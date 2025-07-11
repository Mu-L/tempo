package parquetquery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"

	"github.com/grafana/tempo/pkg/parquetquery/intern"
	"github.com/grafana/tempo/pkg/util"
	pq "github.com/parquet-go/parquet-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// RowNumber is the sequence of row numbers uniquely identifying a value
// in a tree of nested columns, starting at the top-level and including
// another row number for each level of nesting. -1 is a placeholder
// for undefined at lower levels.  RowNumbers can be compared for full
// equality using the == operator, or can be compared partially, looking
// for equal lineages down to a certain level.
// For example given the following tree, the row numbers would be:
//
//	A          0, -1, -1
//	  B        0,  0, -1
//	  C        0,  1, -1
//	    D      0,  1,  0
//	  E        0,  2, -1
//
// Currently supports 8 levels of nesting which should be enough for anybody. :)
type RowNumber [8]int32

const MaxDefinitionLevel = 7

var tracer = otel.Tracer("pkg/parquetquery")

// EmptyRowNumber creates an empty invalid row number.
func EmptyRowNumber() RowNumber {
	return RowNumber{-1, -1, -1, -1, -1, -1, -1, -1}
}

// MaxRowNumber is a helper that represents the maximum(-ish) representable value.
func MaxRowNumber() RowNumber {
	return RowNumber{math.MaxInt32}
}

// CompareRowNumbers compares the sequences of row numbers in
// a and b for partial equality, descending from top-level
// through the given definition level.
// For example, definition level 1 means that row numbers are compared
// at two levels of nesting, the top-level and 1 level of nesting
// below.
func CompareRowNumbers(upToDefinitionLevel int, a, b RowNumber) int {
	for i := 0; i <= upToDefinitionLevel; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// EqualRowNumber compares the sequences of row numbers in a and b
// for partial equality. A little faster than CompareRowNumbers(d,a,b)==0
func EqualRowNumber(upToDefinitionLevel int, a, b RowNumber) bool {
	for i := 0; i <= upToDefinitionLevel; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func truncateRowNumberSlow(definitionLevelToKeep int, t RowNumber) RowNumber {
	n := EmptyRowNumber()
	for i := 0; i <= definitionLevelToKeep; i++ {
		n[i] = t[i]
	}
	return n
}

func TruncateRowNumber(definitionLevelToKeep int, t RowNumber) RowNumber {
	switch definitionLevelToKeep {
	case 0:
		return RowNumber{t[0], -1, -1, -1, -1, -1, -1, -1}
	case 1:
		return RowNumber{t[0], t[1], -1, -1, -1, -1, -1, -1}
	case 2:
		return RowNumber{t[0], t[1], t[2], -1, -1, -1, -1, -1}
	case 3:
		return RowNumber{t[0], t[1], t[2], t[3], -1, -1, -1, -1}
	case 4:
		return RowNumber{t[0], t[1], t[2], t[3], t[4], -1, -1, -1}
	case 5:
		return RowNumber{t[0], t[1], t[2], t[3], t[4], t[5], -1, -1}
	case 6:
		return RowNumber{t[0], t[1], t[2], t[3], t[4], t[5], t[6], -1}
	case 7:
		return RowNumber{t[0], t[1], t[2], t[3], t[4], t[5], t[6], t[7]}
	default:
		panic(fmt.Sprintf("definition level out of bound: should be [0:7] but got %d", definitionLevelToKeep))
	}
}

func (t *RowNumber) Valid() bool {
	return t[0] >= 0
}

// Next increments and resets the row numbers according
// to the given repetition and definition levels. Examples
// from the Dremel whitepaper:
// https://storage.googleapis.com/pub-tools-public-publication-data/pdf/36632.pdf
// Name.Language.Country
// value  | r | d | expected RowNumber
// -------|---|---|-------------------
//
//	|   |   | { -1, -1, -1, -1 }  <-- starting position
//
// us     | 0 | 3 | {  0,  0,  0,  0 }
// null   | 2 | 2 | {  0,  0,  1, -1 }
// null   | 1 | 1 | {  0,  1, -1, -1 }
// gb     | 1 | 3 | {  0,  2,  0,  0 }
// null   | 0 | 1 | {  1,  0, -1, -1 }
func (t *RowNumber) Next(repetitionLevel, definitionLevel, maxDefinitionLevel int) {
	t[repetitionLevel]++

	// New children up through the definition level
	for i := repetitionLevel + 1; i <= definitionLevel; i++ {
		t[i] = 0
	}

	// // Children past the definition level are undefined
	for i := definitionLevel + 1; i < len(t) && i <= maxDefinitionLevel; i++ {
		t[i] = -1
	}
}

// Skip rows at the root-level.
func (t *RowNumber) Skip(numRows int64) {
	t[0] += int32(numRows)
	for i := 1; i < len(t); i++ {
		t[i] = -1
	}
}

// Preceding returns the largest representable row number that is immediately prior to this
// one. Think of it like math.NextAfter but for segmented row numbers. Examples:
//
//		RowNumber 1000.0.0 (defined at 3 levels) is preceded by 999.max.max
//	    RowNumber 1000.-1.-1 (defined at 1 level) is preceded by 999.-1.-1
func (t RowNumber) Preceding() RowNumber {
	for i := len(t) - 1; i >= 0; i-- {
		switch t[i] {
		case -1:
			continue
		case 0:
			t[i] = math.MaxInt32
		default:
			t[i]--
			return t
		}
	}
	return t
}

// IteratorResult is a row of data with a row number and named columns of data.
// Internally it has an unstructured list for efficient collection. The ToMap()
// function can be used to make inspection easier.
type IteratorResult struct {
	RowNumber RowNumber
	Entries   []struct {
		Key   string
		Value pq.Value
	}
	OtherEntries []struct {
		Key   string
		Value interface{}
	}
}

func (r *IteratorResult) Reset() {
	r.Entries = r.Entries[:0]
	r.OtherEntries = r.OtherEntries[:0]
}

func (r *IteratorResult) Append(rr *IteratorResult) {
	if len(rr.Entries) > 0 {
		r.Entries = append(r.Entries, rr.Entries...)
	}
	if len(rr.OtherEntries) > 0 {
		r.OtherEntries = append(r.OtherEntries, rr.OtherEntries...)
	}
}

func (r *IteratorResult) AppendValue(k string, v pq.Value) {
	r.Entries = append(r.Entries, struct {
		Key   string
		Value pq.Value
	}{k, v})
}

func (r *IteratorResult) AppendOtherValue(k string, v interface{}) {
	r.OtherEntries = append(r.OtherEntries, struct {
		Key   string
		Value interface{}
	}{k, v})
}

func (r *IteratorResult) OtherValueFromKey(k string) interface{} {
	for _, e := range r.OtherEntries {
		if e.Key == k {
			return e.Value
		}
	}
	return nil
}

// ToMap converts the unstructured list of data into a map containing an entry
// for each column, and the lists of values.  The order of columns is
// not preseved, but the order of values within each column is.
func (r *IteratorResult) ToMap() map[string][]pq.Value {
	m := map[string][]pq.Value{}
	for _, e := range r.Entries {
		m[e.Key] = append(m[e.Key], e.Value)
	}
	return m
}

// Columns gets the values for each named column. The order of returned values
// matches the order of names given. This is more efficient than converting to a map.
func (r *IteratorResult) Columns(buffer [][]pq.Value, names ...string) [][]pq.Value {
	if cap(buffer) < len(names) {
		buffer = make([][]pq.Value, len(names))
	} else {
		buffer = buffer[:len(names)]
	}
	for i := range buffer {
		buffer[i] = buffer[i][:0]
	}

	for _, e := range r.Entries {
		for i := range names {
			if e.Key == names[i] {
				buffer[i] = append(buffer[i], e.Value)
				break
			}
		}
	}
	return buffer
}

// iterator - Every iterator follows this interface and can be composed.
type Iterator interface {
	fmt.Stringer

	// Next returns nil when done
	Next() (*IteratorResult, error)

	// Like Next but skips over results until reading >= the given location
	SeekTo(t RowNumber, definitionLevel int) (*IteratorResult, error)

	Close()
}

var syncIteratorPool = sync.Pool{
	New: func() interface{} {
		return []pq.Value{}
	},
}

func syncIteratorPoolGet(capacity, len int) []pq.Value {
	res := syncIteratorPool.Get().([]pq.Value)
	if cap(res) < capacity {
		res = make([]pq.Value, capacity)
	}
	res = res[:len]
	return res
}

func syncIteratorPoolPut(b []pq.Value) {
	for i := range b {
		b[i] = pq.Value{}
	}
	syncIteratorPool.Put(b) // nolint: staticcheck
}

type JoinIteratorOption interface {
	applyToJoinIterator(*JoinIterator)
}

type LeftJoinIteratorOption interface {
	applyToLeftJoinIterator(*LeftJoinIterator)
}

type PoolOption struct {
	pool *ResultPool
}

// WithPool allows setting a custom result pool for this iterator. Custom pooling
// can be useful to keep similar sized results together or to isolate data. By
// default all iterators use a shared pool.
func WithPool(p *ResultPool) PoolOption {
	return PoolOption{p}
}

func (o PoolOption) applyToJoinIterator(j *JoinIterator) {
	j.pool = o.pool
}

func (o PoolOption) applyToLeftJoinIterator(j *LeftJoinIterator) {
	j.pool = o.pool
}

type SyncIteratorOpt func(i *SyncIterator)

// SyncIteratorOptIntern enables interning of string values.
// This is useful when the same string value is repeated many times.
// Not recommended with (very) high cardinality columns, such as UUIDs (spanID and traceID).
func SyncIteratorOptIntern() SyncIteratorOpt {
	return func(i *SyncIterator) {
		i.intern = true
		i.interner = intern.New()
	}
}

// SyncIteratorOptPredicate uses the given predicate to filter column values.
func SyncIteratorOptPredicate(p Predicate) SyncIteratorOpt {
	return func(i *SyncIterator) {
		i.filter = p
	}
}

// SyncIteratorOptColumnName sets the column name for the iterator.
// This is used for tracing and debugging only. All work is done
// using the column index which is a required parameter on creation.
func SyncIteratorOptColumnName(columnName string) SyncIteratorOpt {
	return func(i *SyncIterator) {
		i.columnName = columnName
	}
}

// SyncIteratorOptSelectAs returns the values of the columns with this name
// in the IteratorResult. By default the iterator only looks for matches and
// returns their row numbers. This option is used when you also want the actual
// found values back.
func SyncIteratorOptSelectAs(selectAs string) SyncIteratorOpt {
	return func(i *SyncIterator) {
		i.selectAs = selectAs
	}
}

// SyncIteratorOptBufferSize overrides the default buffer size. This is how many
// values are unpacked from the column on each read.
func SyncIteratorOptBufferSize(bufferSize int) SyncIteratorOpt {
	return func(i *SyncIterator) {
		i.readSize = bufferSize
	}
}

// SyncIteratorOptMaxDefinitionLevel specifies the maximum definition level that
// can be expected for this column. Allows for better efficiency, but not
// required for correct behavior.
func SyncIteratorOptMaxDefinitionLevel(maxDefinitionLevel int) SyncIteratorOpt {
	return func(i *SyncIterator) {
		i.maxDefinitionLevel = maxDefinitionLevel
	}
}

// SyncIterator is a synchronous column iterator. It scans through the given row
// groups and column, and applies the optional predicate to each chunk, page, and value.
// Results are read by calling Next() until it returns nil.
type SyncIterator struct {
	// Config
	column     int
	columnName string
	selectAs   string
	rgs        []pq.RowGroup
	rgsMin     []RowNumber
	rgsMax     []RowNumber // Exclusive, row number of next one past the row group
	readSize   int
	filter     Predicate

	// Status
	span            trace.Span
	curr            RowNumber
	currRowGroup    pq.RowGroup
	currRowGroupMin RowNumber
	currRowGroupMax RowNumber
	currChunk       *ColumnChunkHelper
	currPage        pq.Page
	currPageMin     RowNumber
	currPageMax     RowNumber
	currValues      pq.ValueReader
	currBuf         []pq.Value
	currBufN        int
	currPageN       int
	at              IteratorResult // Current value pointed at by iterator. Returned by call Next and SeekTo, valid until next call.

	maxDefinitionLevel int

	intern   bool
	interner *intern.Interner
}

var _ Iterator = (*SyncIterator)(nil)

// NewSyncIterator iterates values in a column of a parquet file. Required values
// are the numeric column index and the row groups to iterate over.  The column index
// can be found by name using GetColumnIndexByPath.  By default it does the minimal
// amount of work, which is to scan for matches (using the given predicate if specified),
// and return their row values. To retrieve the found values back, pass SyncIteratorOptSelectAs.
//
// Not safe for concurrent use.
func NewSyncIterator(ctx context.Context, rgs []pq.RowGroup, column int, opts ...SyncIteratorOpt) *SyncIterator {
	// Assign row group bounds.
	// Lower bound is inclusive
	// Upper bound is exclusive, points at the first row of the next group
	rn := EmptyRowNumber()
	rgsMin := make([]RowNumber, len(rgs))
	rgsMax := make([]RowNumber, len(rgs))
	for i, rg := range rgs {
		rgsMin[i] = rn
		rgsMax[i] = rn
		rgsMax[i].Skip(rg.NumRows() + 1)
		rn.Skip(rg.NumRows())
	}

	// Create the iterator
	i := &SyncIterator{
		column:             column,
		rgs:                rgs,
		readSize:           1000, // default value
		rgsMin:             rgsMin,
		rgsMax:             rgsMax,
		curr:               EmptyRowNumber(),
		at:                 IteratorResult{},
		maxDefinitionLevel: MaxDefinitionLevel, // default value
	}

	// Apply options
	for _, opt := range opts {
		opt(i)
	}

	if i.selectAs != "" {
		// Preallocate 1 entry with the given name.
		i.at.Entries = []struct {
			Key   string
			Value pq.Value
		}{
			{Key: i.selectAs},
		}
	}

	_, i.span = tracer.Start(ctx, "syncIterator", trace.WithAttributes(
		attribute.Int("columnIndex", column),
		attribute.String("column", i.columnName),
	))

	return i
}

func (c *SyncIterator) String() string {
	filter := "nil"
	if c.filter != nil {
		filter = c.filter.String()
	}
	return fmt.Sprintf("SyncIterator: %s : %s", c.columnName, filter)
}

func (c *SyncIterator) Next() (*IteratorResult, error) {
	rn, v, err := c.next()
	if err != nil {
		return nil, err
	}
	if !rn.Valid() {
		return nil, nil
	}
	return c.makeResult(rn, v), nil
}

// SeekTo moves this iterator to the next result that is greater than
// or equal to the given row number (and based on the given definition level)
func (c *SyncIterator) SeekTo(to RowNumber, definitionLevel int) (*IteratorResult, error) {
	if c.seekRowGroup(to, definitionLevel) {
		return nil, nil
	}

	done, err := c.seekPages(to, definitionLevel)
	if err != nil {
		return nil, err
	}
	if done {
		return nil, nil
	}

	c.seekWithinPage(to, definitionLevel)

	// The row group and page have been selected to where this value is possibly
	// located. Now scan through the page and look for it.
	for {
		rn, v, err := c.next()
		if err != nil {
			return nil, err
		}
		if !rn.Valid() {
			return nil, nil
		}

		if CompareRowNumbers(definitionLevel, rn, to) >= 0 {
			return c.makeResult(rn, v), nil
		}
	}
}

func (c *SyncIterator) popRowGroup() (pq.RowGroup, RowNumber, RowNumber) {
	if len(c.rgs) == 0 {
		return nil, EmptyRowNumber(), EmptyRowNumber()
	}

	rg := c.rgs[0]
	min := c.rgsMin[0]
	max := c.rgsMax[0]

	c.rgs = c.rgs[1:]
	c.rgsMin = c.rgsMin[1:]
	c.rgsMax = c.rgsMax[1:]

	return rg, min, max
}

// seekRowGroup skips ahead to the row group that could contain the value at the
// desired row number. Does nothing if the current row group is already the correct one.
func (c *SyncIterator) seekRowGroup(seekTo RowNumber, definitionLevel int) (done bool) {
	if c.currRowGroup != nil && CompareRowNumbers(definitionLevel, seekTo, c.currRowGroupMax) >= 0 {
		// Done with this row group
		c.closeCurrRowGroup()
	}

	for c.currRowGroup == nil {

		rg, min, max := c.popRowGroup()
		if rg == nil {
			return true
		}

		if CompareRowNumbers(definitionLevel, seekTo, max) != -1 {
			continue
		}

		cc := &ColumnChunkHelper{ColumnChunk: rg.ColumnChunks()[c.column]}
		if c.filter != nil && !c.filter.KeepColumnChunk(cc) {
			cc.Close()
			continue
		}

		// This row group matches both row number and filter.
		c.setRowGroup(rg, min, max, cc)
	}

	return c.currRowGroup == nil
}

// seekPages skips ahead in the current row group to the page that could contain the value at
// the desired row number. Does nothing if the current page is already the correct one.
func (c *SyncIterator) seekPages(seekTo RowNumber, definitionLevel int) (done bool, err error) {
	if c.currPage != nil && CompareRowNumbers(definitionLevel, seekTo, c.currPageMax) >= 0 {
		// Value not in this page
		c.setPage(nil)
	}

	if c.currPage == nil {
		// TODO (mdisibio)   :((((((((
		//    pages.SeekToRow is more costly than expected.  It doesn't reuse existing i/o
		// so it can't be called naively every time we swap pages. We need to figure out
		// a way to determine when it is worth calling here.
		/*
			// Seek into the pages. This is relative to the start of the row group
			if seekTo[0] > 0 {
				// Determine row delta. We subtract 1 because curr points at the previous row
				skip := seekTo[0] - c.currRowGroupMin[0] - 1
				if skip > 0 {
					if err := c.currPages.SeekToRow(skip); err != nil {
						return true, err
					}
					c.curr.Skip(skip)
				}
			}*/

		for c.currPage == nil {
			pg, err := c.currChunk.NextPage()
			if pg == nil || err != nil {
				// No more pages in this column chunk,
				// cleanup and exit.
				if errors.Is(err, io.EOF) {
					err = nil
				}
				pq.Release(pg)
				c.closeCurrRowGroup()
				return true, err
			}

			// Skip based on row number?
			newRN := c.curr
			newRN.Skip(pg.NumRows() + 1)
			if CompareRowNumbers(definitionLevel, seekTo, newRN) >= 0 {
				c.curr.Skip(pg.NumRows())
				pq.Release(pg)
				continue
			}

			// Skip based on filter?
			if c.filter != nil && !c.filter.KeepPage(pg) {
				c.curr.Skip(pg.NumRows())
				pq.Release(pg)
				continue
			}

			c.setPage(pg)
		}
	}

	return false, nil
}

// seekWithinPage decides if it should reslice the current page to jump directly to the desired row number
// or allow the iterator to call Next() until it finds the desired row number. it uses the magicThreshold
// as its balance point. if the number of Next()s to skip is less than the magicThreshold, it will not reslice
func (c *SyncIterator) seekWithinPage(to RowNumber, definitionLevel int) {
	rowSkipRelative := int(to[0] - c.curr[0])
	if rowSkipRelative == 0 {
		return
	}

	const magicThreshold = 1000
	shouldSkip := false

	if definitionLevel == 0 {
		// if definition level is 0 there is always a 1:1 ratio between Next()s and rows. it's only deeper
		// levels of nesting we have to manually count
		shouldSkip = rowSkipRelative > magicThreshold
	} else {
		// this is a nested iterator, let's count the Next()s required to get to the desired row number
		// and decide if we should skip or not
		replvls := c.currPage.RepetitionLevels()
		nextsRequired := 0

		for i := c.currPageN; i < len(replvls); i++ {
			nextsRequired++

			if nextsRequired > magicThreshold {
				shouldSkip = true
				break
			}

			if replvls[i] == 0 { // 0 rep lvl indicates a new row
				rowSkipRelative-- // decrement the number of rows we need to skip
				if rowSkipRelative <= 0 {
					// if we hit here we skipped all rows and did not exceed the magic threshold, so we're leaving shouldSkip false
					break
				}
			}
		}
	}

	if !shouldSkip {
		return
	}

	// skips are calculated off the start of the page
	rowSkip := to[0] - c.currPageMin[0]
	if rowSkip < 1 {
		return
	}
	if rowSkip > int32(c.currPage.NumRows()) {
		return
	}

	// reslice the page to jump directly to the desired row number
	pg := c.currPage.Slice(int64(rowSkip-1), c.currPage.NumRows())

	// remove all detail below the row number
	c.curr = TruncateRowNumber(0, to)
	c.curr = c.curr.Preceding()

	// reset buffers and other vars
	pq.Release(c.currPage)
	c.currPage = pg
	c.currPageMin = c.curr
	c.currValues = pg.Values()
	c.currPageN = 0
	syncIteratorPoolPut(c.currBuf)
	c.currBuf = nil
}

// next is the core functionality of this iterator and returns the next matching result. This
// may involve inspecting multiple row groups, pages, and values until a match is found. When
// we run out of things to inspect, it returns nil. The reason this method is distinct from
// Next() is because it doesn't wrap the results in an IteratorResult, which is more efficient
// when being called multiple times and throwing away the results like in SeekTo().
func (c *SyncIterator) next() (RowNumber, *pq.Value, error) {
	for {
		if c.currRowGroup == nil {
			rg, min, max := c.popRowGroup()
			if rg == nil {
				return EmptyRowNumber(), nil, nil
			}

			cc := &ColumnChunkHelper{ColumnChunk: rg.ColumnChunks()[c.column]}
			if c.filter != nil && !c.filter.KeepColumnChunk(cc) {
				cc.Close()
				continue
			}

			c.setRowGroup(rg, min, max, cc)
		}

		if c.currPage == nil {
			pg, err := c.currChunk.NextPage()
			if err != nil && !errors.Is(err, io.EOF) {
				return EmptyRowNumber(), nil, err
			}
			if pg == nil || errors.Is(err, io.EOF) {
				// This row group is exhausted
				c.closeCurrRowGroup()
				continue
			}
			if c.filter != nil && !c.filter.KeepPage(pg) {
				// This page filtered out
				c.curr.Skip(pg.NumRows())
				pq.Release(pg)
				continue
			}
			c.setPage(pg)
		}

		// Read next batch of values if needed
		if c.currBuf == nil {
			c.currBuf = syncIteratorPoolGet(c.readSize, 0)
		}
		if c.currBufN >= len(c.currBuf) || len(c.currBuf) == 0 {
			c.currBuf = c.currBuf[:cap(c.currBuf)]
			n, err := c.currValues.ReadValues(c.currBuf)
			if err != nil && !errors.Is(err, io.EOF) {
				return EmptyRowNumber(), nil, err
			}
			c.currBuf = c.currBuf[:n]
			c.currBufN = 0
			if n == 0 {
				// This value reader and page are exhausted.
				c.setPage(nil)
				continue
			}
		}

		// Consume current buffer until empty
		for c.currBufN < len(c.currBuf) {
			v := &c.currBuf[c.currBufN]

			// Inspect all values to track the current row number,
			// even if the value is filtered out next.
			c.curr.Next(v.RepetitionLevel(), v.DefinitionLevel(), c.maxDefinitionLevel)
			c.currBufN++
			c.currPageN++

			if c.filter != nil && !c.filter.KeepValue(*v) {
				continue
			}

			return c.curr, v, nil
		}
	}
}

func (c *SyncIterator) setRowGroup(rg pq.RowGroup, min, max RowNumber, cc *ColumnChunkHelper) {
	c.closeCurrRowGroup()
	c.curr = min
	c.currRowGroup = rg
	c.currRowGroupMin = min
	c.currRowGroupMax = max
	c.currChunk = cc
}

func (c *SyncIterator) setPage(pg pq.Page) {
	// Handle an outgoing page
	if c.currPage != nil {
		c.curr = c.currPageMax.Preceding() // Reposition current row number to end of this page.
		pq.Release(c.currPage)
		c.currPage = nil
	}

	// Reset value buffers
	c.currValues = nil
	c.currPageMax = EmptyRowNumber()
	c.currPageMin = EmptyRowNumber()
	c.currBufN = 0
	c.currPageN = 0

	// If we don't immediately have a new incoming page
	// then return the buffer to the pool.
	if pg == nil && c.currBuf != nil {
		syncIteratorPoolPut(c.currBuf)
		c.currBuf = nil
	}

	// Handle an incoming page
	if pg != nil {
		rn := c.curr
		rn.Skip(pg.NumRows() + 1) // Exclusive upper bound, points at the first rownumber in the next page
		c.currPage = pg
		c.currPageMin = c.curr
		c.currPageMax = rn
		c.currValues = pg.Values()
	}
}

func (c *SyncIterator) closeCurrRowGroup() {
	if c.currChunk != nil {
		c.currChunk.Close()
	}

	c.currRowGroup = nil
	c.currRowGroupMin = EmptyRowNumber()
	c.currRowGroupMax = EmptyRowNumber()
	c.currChunk = nil
	c.setPage(nil)
}

func (c *SyncIterator) makeResult(t RowNumber, v *pq.Value) *IteratorResult {
	// Use same static result instead of pooling
	c.at.RowNumber = t

	// The length of the Entries slice indicates if we should return the
	// value or just the row number. This has already been checked during
	// creation. SyncIterator reads a single column so the slice will
	// always have length 0 or 1.
	if len(c.at.Entries) == 1 {
		if c.intern {
			c.at.Entries[0].Value = c.interner.UnsafeClone(v)
		} else {
			c.at.Entries[0].Value = v.Clone()
		}
	}

	return &c.at
}

func (c *SyncIterator) Close() {
	c.closeCurrRowGroup()

	c.span.End()

	if c.intern && c.interner != nil {
		c.interner.Close()
	}
}

// JoinIterator joins two or more iterators for matches at the given definition level.
// I.e. joining at definitionLevel=0 means that each iterator must produce a result
// within the same root node.
type JoinIterator struct {
	definitionLevel int
	iters           []Iterator
	peeks           []*IteratorResult
	pred            GroupPredicate
	pool            *ResultPool
	at              *IteratorResult
}

var _ Iterator = (*JoinIterator)(nil)

func NewJoinIterator(definitionLevel int, iters []Iterator, pred GroupPredicate, opts ...JoinIteratorOption) *JoinIterator {
	j := &JoinIterator{
		definitionLevel: definitionLevel,
		iters:           iters,
		peeks:           make([]*IteratorResult, len(iters)),
		pred:            pred,
		pool:            DefaultPool,
	}

	for _, opt := range opts {
		opt.applyToJoinIterator(j)
	}

	j.at = j.pool.Get()

	return j
}

func (j *JoinIterator) String() string {
	var iters string
	for _, iter := range j.iters {
		iters += "\n\t" + util.TabOut(iter)
	}
	return fmt.Sprintf("JoinIterator: %d: %s\t%s)", j.definitionLevel, j.pred, iters)
}

func (j *JoinIterator) Next() (*IteratorResult, error) {
outer:
	for {
		// This loop is doing two things:
		// On first-pass peek each iter and ensure it has at least one
		// result.  If any iter has no results we can exit early
		// without processing the remaining data in the others.
		// On subsequent passes the first iter is never nil except
		// when everything is fully exhausted. We check once more
		// and then exit.
		if j.peeks[0] == nil {
			for i := range j.iters {
				res, err := j.peek(i)
				if err != nil {
					return nil, err
				}
				if res == nil {
					return nil, nil
				}
			}
		}

		// The first iter is pointing at the next candidate row. Proceed through iters 2 to N looking
		// for matches.
		for iterNum := 1; iterNum < len(j.iters); iterNum++ {
			err := j.seek(iterNum, j.peeks[0].RowNumber, j.definitionLevel)
			if err != nil {
				return nil, err
			}

			if j.peeks[iterNum] == nil {
				return nil, nil
			}

			if CompareRowNumbers(j.definitionLevel, j.peeks[iterNum].RowNumber, j.peeks[0].RowNumber) == 1 {
				// This iterator has a higher row number than all previous iterators.  That means it might have
				// a higher filtering power, swap it to the top and restart the loop.
				j.iters[0], j.iters[iterNum] = j.iters[iterNum], j.iters[0]
				j.peeks[0], j.peeks[iterNum] = j.peeks[iterNum], j.peeks[0]
				continue outer
			}
		}

		// All iterators pointing at same row
		// Get the data
		result, err := j.collect(j.peeks[0].RowNumber)
		if err != nil {
			return nil, fmt.Errorf("join iterator collect failed: %w", err)
		}

		// Keep group?
		if j.pred == nil || j.pred.KeepGroup(result) {
			// Yes
			return result, nil
		}
	}
}

func (j *JoinIterator) SeekTo(t RowNumber, d int) (*IteratorResult, error) {
	err := j.seekAll(t, d)
	if err != nil {
		return nil, fmt.Errorf("join iterator seekAll failed: %w", err)
	}
	return j.Next()
}

func (j *JoinIterator) seek(iterNum int, t RowNumber, d int) error {
	var err error
	t = TruncateRowNumber(d, t)
	if j.peeks[iterNum] == nil || CompareRowNumbers(d, j.peeks[iterNum].RowNumber, t) == -1 {
		j.peeks[iterNum], err = j.iters[iterNum].SeekTo(t, d)
		if err != nil {
			return err
		}
	}
	return nil
}

func (j *JoinIterator) seekAll(t RowNumber, d int) error {
	var err error
	t = TruncateRowNumber(d, t)
	for iterNum, iter := range j.iters {
		if j.peeks[iterNum] == nil || CompareRowNumbers(d, j.peeks[iterNum].RowNumber, t) == -1 {
			j.peeks[iterNum], err = iter.SeekTo(t, d)
			if err != nil {
				return err
			}
			if j.peeks[iterNum] == nil {
				// This iterator is exhausted, no reason to seek the remaining
				break
			}
		}
	}
	return nil
}

func (j *JoinIterator) peek(iterNum int) (*IteratorResult, error) {
	var err error
	if j.peeks[iterNum] == nil {
		j.peeks[iterNum], err = j.iters[iterNum].Next()
		if err != nil {
			return nil, err
		}
	}
	return j.peeks[iterNum], nil
}

// Collect data from the given iterators until they point at
// the next row (according to the configured definition level)
// or are exhausted.
func (j *JoinIterator) collect(rowNumber RowNumber) (*IteratorResult, error) {
	var err error

	result := j.at
	result.Reset()
	result.RowNumber = rowNumber

	for i := range j.iters {
		for j.peeks[i] != nil && EqualRowNumber(j.definitionLevel, j.peeks[i].RowNumber, rowNumber) {
			result.Append(j.peeks[i])
			j.peeks[i], err = j.iters[i].Next()
			if err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func (j *JoinIterator) Close() {
	for _, i := range j.iters {
		i.Close()
	}
	j.pool.Release(j.at)
}

// LeftJoinIterator joins two or more iterators for matches at the given definition level.
// The first set of required iterators must all produce matching results. The second set
// of optional iterators are collected if they also match.
// TODO - This should technically obsolete the JoinIterator.
type LeftJoinIterator struct {
	definitionLevel              int
	required, optional           []Iterator
	peeksRequired, peeksOptional []*IteratorResult
	pred                         GroupPredicate
	pool                         *ResultPool
	at                           *IteratorResult
}

var _ Iterator = (*LeftJoinIterator)(nil)

func NewLeftJoinIterator(definitionLevel int, required, optional []Iterator, pred GroupPredicate, opts ...LeftJoinIteratorOption) (*LeftJoinIterator, error) {
	// No query should ever result in a left-join with no required iterators.
	// If this happens, it's a bug in the iter building code.
	// LeftJoinIterator is not designed to handle this case and will loop forever.
	if len(required) == 0 {
		return nil, fmt.Errorf("left join iterator requires at least one required iterator")
	}

	j := &LeftJoinIterator{
		definitionLevel: definitionLevel,
		required:        required,
		optional:        optional,
		peeksRequired:   make([]*IteratorResult, len(required)),
		peeksOptional:   make([]*IteratorResult, len(optional)),
		pred:            pred,
		pool:            DefaultPool,
	}

	for _, opt := range opts {
		opt.applyToLeftJoinIterator(j)
	}

	j.at = j.pool.Get()

	return j, nil
}

func (j *LeftJoinIterator) String() string {
	srequired := "required: "
	for _, r := range j.required {
		srequired += "\n\t" + util.TabOut(r)
	}
	soptional := "optional: "
	for _, o := range j.optional {
		soptional += "\n\t" + util.TabOut(o)
	}
	return fmt.Sprintf("LeftJoinIterator: %d: %s\n%s\n%s", j.definitionLevel, j.pred, srequired, soptional)
}

func (j *LeftJoinIterator) Next() (*IteratorResult, error) {
outer:
	for {
		// This loop is doing two things:
		// On first-pass peek each required iter and ensure it has
		// at least one result.  If any iter has no results we can
		// exit early without processing the remaining data in the others.
		// On subsequent passes the first iter is never nil except
		// when everything is fully exhausted. We check once more
		// and then exit.
		if j.peeksRequired[0] == nil {
			for i := range j.peeksRequired {
				res, err := j.peek(i)
				if err != nil {
					return nil, err
				}
				if res == nil {
					return nil, nil
				}
			}
		}

		// The first iter is pointing at the next candidate row. Proceed through iters 2 to N looking
		// for matches.
		for iterNum := 1; iterNum < len(j.required); iterNum++ {
			err := j.seek(iterNum, j.peeksRequired[0].RowNumber, j.definitionLevel)
			if err != nil {
				return nil, err
			}

			if j.peeksRequired[iterNum] == nil {
				// This iterator is exhausted no more joins possible.
				return nil, nil
			}

			if CompareRowNumbers(j.definitionLevel, j.peeksRequired[iterNum].RowNumber, j.peeksRequired[0].RowNumber) == 1 {
				// This iterator has a higher row number than all previous iterators.  That means it might have
				// a higher filtering power, swap it to the top and restart the loop.
				j.required[0], j.required[iterNum] = j.required[iterNum], j.required[0]
				j.peeksRequired[0], j.peeksRequired[iterNum] = j.peeksRequired[iterNum], j.peeksRequired[0]
				continue outer
			}
		}

		// All iterators pointing at same row
		// Get the data
		result, err := j.collect(j.peeksRequired[0].RowNumber)
		if err != nil {
			return nil, err
		}

		// Keep group?
		if j.pred == nil || j.pred.KeepGroup(result) {
			// Yes
			return result, nil
		}
	}
}

func (j *LeftJoinIterator) SeekTo(t RowNumber, d int) (*IteratorResult, error) {
	done, err := j.seekAllRequired(t, d)
	if err != nil {
		return nil, err
	}

	if done {
		// A required iterator is exhausted, no reason to seek the remaining
		return nil, nil
	}

	err = j.seekAllOptional(t, d)
	if err != nil {
		return nil, err
	}

	return j.Next()
}

func (j *LeftJoinIterator) seek(iterNum int, t RowNumber, d int) (err error) {
	if j.peeksRequired[iterNum] == nil || CompareRowNumbers(d, j.peeksRequired[iterNum].RowNumber, t) == -1 {
		j.peeksRequired[iterNum], err = j.required[iterNum].SeekTo(t, d)
		if err != nil {
			return
		}
	}
	return nil
}

func (j *LeftJoinIterator) seekAllRequired(t RowNumber, d int) (done bool, err error) {
	for iterNum, iter := range j.required {
		if j.peeksRequired[iterNum] == nil || CompareRowNumbers(d, j.peeksRequired[iterNum].RowNumber, t) == -1 {
			j.peeksRequired[iterNum], err = iter.SeekTo(t, d)
			if err != nil {
				return
			}
			if j.peeksRequired[iterNum] == nil {
				// A required iterator is exhausted, no reason to seek the remaining
				return true, nil
			}
		}
	}
	return
}

func (j *LeftJoinIterator) seekAllOptional(t RowNumber, d int) (err error) {
	for iterNum, iter := range j.optional {
		if j.peeksOptional[iterNum] == nil || CompareRowNumbers(d, j.peeksOptional[iterNum].RowNumber, t) == -1 {
			j.peeksOptional[iterNum], err = iter.SeekTo(t, d)
			if err != nil {
				return
			}
		}
	}
	return nil
}

func (j *LeftJoinIterator) peek(iterNum int) (*IteratorResult, error) {
	var err error
	if j.peeksRequired[iterNum] == nil {
		j.peeksRequired[iterNum], err = j.required[iterNum].Next()
		if err != nil {
			return nil, err
		}
	}
	return j.peeksRequired[iterNum], nil
}

// Collect data from the given iterators until they point at
// the next row (according to the configured definition level)
// or are exhausted.
func (j *LeftJoinIterator) collect(rowNumber RowNumber) (*IteratorResult, error) {
	var err error

	result := j.at
	result.Reset()
	result.RowNumber = rowNumber

	collect := func(iters []Iterator, peeks []*IteratorResult) {
		for i := range iters {
			// Collect matches
			for peeks[i] != nil && EqualRowNumber(j.definitionLevel, peeks[i].RowNumber, rowNumber) {
				result.Append(peeks[i])
				peeks[i], err = iters[i].Next()
				if err != nil {
					return
				}
			}
		}
	}

	// Collect is only called after we have found a match among all
	// required iterators, therefore we only need to seek the optional ones to same location.
	err = j.seekAllOptional(rowNumber, j.definitionLevel)
	if err != nil {
		return nil, err
	}

	collect(j.required, j.peeksRequired)
	if err != nil {
		return nil, err
	}

	collect(j.optional, j.peeksOptional)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (j *LeftJoinIterator) Close() {
	for _, i := range j.required {
		i.Close()
	}
	for _, i := range j.optional {
		i.Close()
	}
	j.pool.Release(j.at)
}

// UnionIterator produces all results for all given iterators.  When iterators
// align to the same row, based on the configured definition level, then the results
// are returned together. Else the next matching iterator is returned.
type UnionIterator struct {
	definitionLevel int
	iters           []Iterator
	lowestIters     []int
	peeks           []*IteratorResult
	pred            GroupPredicate
	at              IteratorResult
}

var _ Iterator = (*UnionIterator)(nil)

func NewUnionIterator(definitionLevel int, iters []Iterator, pred GroupPredicate) *UnionIterator {
	j := UnionIterator{
		definitionLevel: definitionLevel,
		iters:           iters,
		lowestIters:     make([]int, len(iters)),
		peeks:           make([]*IteratorResult, len(iters)),
		pred:            pred,
	}
	return &j
}

func (u *UnionIterator) String() string {
	var iters string
	for _, iter := range u.iters {
		iters += "\n\t" + util.TabOut(iter)
	}
	return fmt.Sprintf("UnionIterator: %d: %s\t%s)", u.definitionLevel, u.pred, iters)
}

func (u *UnionIterator) Next() (*IteratorResult, error) {
	// Here is the algorithm for unions:  On each pass of the iterators
	// we remember which ones are pointing at the earliest same row. The
	// lowest iterators are then collected and a result is produced. Keep
	// going until all iterators are exhausted.
	for {
		lowestRowNumber := MaxRowNumber()
		u.lowestIters = u.lowestIters[:0]

		for iterNum := range u.iters {
			rn, err := u.peek(iterNum)
			if err != nil {
				return nil, fmt.Errorf("union iterator peek failed: %w", err)
			}
			// If this iterator is exhausted go to the next one
			if rn == nil {
				continue
			}

			c := CompareRowNumbers(u.definitionLevel, rn.RowNumber, lowestRowNumber)
			switch c {
			case -1:
				// New lowest
				u.lowestIters = u.lowestIters[:0]
				lowestRowNumber = rn.RowNumber
				fallthrough

			case 0:
				// Same
				u.lowestIters = append(u.lowestIters, iterNum)
			}
		}

		// Consume lowest iterators
		result, err := u.collect(u.lowestIters, lowestRowNumber)
		if err != nil {
			return nil, fmt.Errorf("union iterator collect failed: %w", err)
		}

		// After each pass it is guaranteed to have found something
		// from at least one iterator, or all are exhausted
		if len(u.lowestIters) > 0 {
			if u.pred != nil && !u.pred.KeepGroup(result) {
				continue
			}

			return result, nil
		}

		// All exhausted
		return nil, nil
	}
}

func (u *UnionIterator) SeekTo(t RowNumber, d int) (*IteratorResult, error) {
	var err error
	t = TruncateRowNumber(d, t)
	for iterNum, iter := range u.iters {
		if p := u.peeks[iterNum]; p == nil || CompareRowNumbers(d, p.RowNumber, t) == -1 {
			u.peeks[iterNum], err = iter.SeekTo(t, d)
			if err != nil {
				return nil, fmt.Errorf("union iterator seek to failed: %w", err)
			}
		}
	}
	return u.Next()
}

func (u *UnionIterator) peek(iterNum int) (*IteratorResult, error) {
	var err error
	if u.peeks[iterNum] == nil {
		u.peeks[iterNum], err = u.iters[iterNum].Next()
		if err != nil {
			return nil, err
		}
	}
	return u.peeks[iterNum], err
}

// Collect data from the given iterators until they point at
// the next row (according to the configured definition level)
// or are exhausted.
func (u *UnionIterator) collect(iterNums []int, rowNumber RowNumber) (*IteratorResult, error) {
	var err error

	result := &u.at
	result.Reset()
	result.RowNumber = rowNumber

	for _, iterNum := range iterNums {
		for u.peeks[iterNum] != nil && EqualRowNumber(u.definitionLevel, u.peeks[iterNum].RowNumber, rowNumber) {
			result.Append(u.peeks[iterNum])
			u.peeks[iterNum], err = u.iters[iterNum].Next()
			if err != nil {
				return nil, err
			}
		}
	}

	return result, err
}

func (u *UnionIterator) Close() {
	for _, i := range u.iters {
		i.Close()
	}
}

type GroupPredicate interface {
	fmt.Stringer

	KeepGroup(*IteratorResult) bool
}

// KeyValueGroupPredicate takes key/value pairs and checks if the
// group contains all of them. This is the only predicate/iterator
// that is knowledgable about our trace or search contents. I'd like
// to change that and make it generic, but it's quite complex and not
// figured it out yet.
type KeyValueGroupPredicate struct {
	keys   [][]byte
	vals   [][]byte
	buffer [][]pq.Value
}

var _ GroupPredicate = (*KeyValueGroupPredicate)(nil)

func NewKeyValueGroupPredicate(keys, values []string) *KeyValueGroupPredicate {
	// Pre-convert all to bytes
	p := &KeyValueGroupPredicate{}
	for _, k := range keys {
		p.keys = append(p.keys, []byte(k))
	}
	for _, v := range values {
		p.vals = append(p.vals, []byte(v))
	}
	return p
}

func (a *KeyValueGroupPredicate) String() string {
	var skeys []string
	var svals []string
	for _, k := range a.keys {
		skeys = append(skeys, string(k))
	}
	for _, v := range a.vals {
		svals = append(svals, string(v))
	}
	return fmt.Sprintf("KeyValueGroupPredicate{%v, %v}", skeys, svals)
}

// KeepGroup checks if the given group contains all of the requested
// key/value pairs.
func (a *KeyValueGroupPredicate) KeepGroup(group *IteratorResult) bool {
	// printGroup(group)
	a.buffer = group.Columns(a.buffer, "keys", "values")

	keys, vals := a.buffer[0], a.buffer[1]

	if len(keys) < len(a.keys) || len(keys) != len(vals) {
		// Missing data or unsatisfiable condition
		return false
	}

	/*fmt.Println("Inspecting group:")
	for i := 0; i < len(keys); i++ {
		fmt.Printf("%d: %s = %s \n", i, keys[i].String(), vals[i].String())
	}*/

	for i := 0; i < len(a.keys); i++ {
		k := a.keys[i]
		v := a.vals[i]

		// Make sure k and v exist somewhere
		found := false

		for j := 0; j < len(keys) && j < len(vals); j++ {
			if bytes.Equal(k, keys[j].ByteArray()) && bytes.Equal(v, vals[j].ByteArray()) {
				found = true
				break
			}
		}

		if !found {
			return false
		}
	}
	return true
}

func panicWhenInvalidDefinitionLevel(definitionLevel int) {
	panic(fmt.Sprintf("definition level out of bound: should be [0:7] but got %d", definitionLevel))
}

/*func printGroup(g *iteratorResult) {
	fmt.Println("---group---")
	for _, e := range g.entries {
		fmt.Println("key:", e.k)
		fmt.Println(" : ", e.v.String())
	}
}*/
