package inspect

import (
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/grafana/tempo/pkg/parquetinspect/output"
	"github.com/parquet-go/parquet-go"
)

var aggregateCellFields = [...]string{"size", "values", "nulls"}

type AggregateCellStats struct {
	Column string `json:"col"`
	Size   int    `json:"size"`
	Values int    `json:"values"`
	Nulls  int    `json:"nulls"`
}

type Aggregate struct {
	Value string               `json:"value"`
	Stats []AggregateCellStats `json:"stats"`
}

func (rs *Aggregate) SerializableData() any {
	return rs
}

func (rs *Aggregate) Cells() []any {
	cells := make([]any, 0, len(rs.Stats)*len(aggregateCellFields)+1)
	cells = append(cells, rs.Value)
	for _, c := range rs.Stats {
		cells = append(cells, c.Size, c.Values, c.Nulls)
	}
	return cells
}

type AggregateOptions struct {
	GroupByColumn int
	Columns       []int
}

func NewAggregateCalculator(file *parquet.File, options AggregateOptions) (*AggregateCalculator, error) {
	all := LeafColumns(file)

	idx := options.GroupByColumn
	if idx >= len(all) {
		return nil, fmt.Errorf("group by column index expected to be lower than %d but was %d", idx, len(all))
	}
	groupByColumn := all[idx]

	var columns []*parquet.Column
	if len(options.Columns) == 0 {
		for _, col := range all {
			if col.MaxDefinitionLevel() >= groupByColumn.MaxDefinitionLevel() {
				columns = append(columns, col)
			}
		}
	} else {
		columns = make([]*parquet.Column, 0, len(options.Columns))
		for _, idx := range options.Columns {
			if idx >= len(all) {
				return nil, fmt.Errorf("column index expectd be lower than %d but was %d", idx, len(all))
			}
			col := all[idx]
			if col.MaxDefinitionLevel() < groupByColumn.MaxDefinitionLevel() {
				return nil, fmt.Errorf(
					"column max definition level expected to be greater or equal than %d but was %d",
					groupByColumn.MaxDefinitionLevel(), col.MaxRepetitionLevel())
			}
			columns = append(columns, col)
		}
	}

	header := make([]any, 0, len(columns)*len(aggregateCellFields)+1)
	header = append(header, groupByColumn.Name()+" values")
	for _, col := range columns {
		header = append(header, fmt.Sprintf("%d/%s: %s", col.Index(), col.Name(), aggregateCellFields[0]), aggregateCellFields[1], aggregateCellFields[2])
	}

	c := AggregateCalculator{header: header}
	err := c.calculateResults(groupByColumn, columns)
	if err != nil {
		return nil, fmt.Errorf("unable to calculate results: %w", err)
	}

	return &c, nil
}

type AggregateCalculator struct {
	header    []any
	result    []*Aggregate
	resultIdx int
}

func (c *AggregateCalculator) Header() []any {
	return c.header
}

func (c *AggregateCalculator) NextRow() (output.TableRow, error) {
	if c.resultIdx >= len(c.result) {
		return nil, fmt.Errorf("no more aggregate results: %w", io.EOF)
	}

	r := c.result[c.resultIdx]
	c.resultIdx++

	return r, nil
}

func (c *AggregateCalculator) calculateResults(groupByColumn *parquet.Column, columns []*parquet.Column) error {
	// setup column iterators
	groupByIter, err := newGroupingColumnIterator(groupByColumn, groupByColumn, Pagination{})
	if err != nil {
		return fmt.Errorf("unable to create aggregate calculator: %w", err)
	}

	var columnIter []*groupingColumnIterator
	for _, col := range columns {
		it, err := newGroupingColumnIterator(col, groupByColumn, Pagination{})
		if err != nil {
			return fmt.Errorf("unable to create aggregate calculator: %w", err)
		}
		columnIter = append(columnIter, it)
	}

	// calculate aggregated result map
	resultMap := make(map[string]*Aggregate)
	for {
		groupByVals, err := groupByIter.NextGroup()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if len(groupByVals) != 1 {
			return errors.New("group by iterator expected to return exactly one value")
		}
		groupByVal := groupByVals[0]

		aggregate, ok := resultMap[groupByVal.String()]
		if !ok {
			aggregate = &Aggregate{
				Value: groupByVal.String(),
				Stats: make([]AggregateCellStats, len(columns)),
			}
			for i, col := range columns {
				aggregate.Stats[i].Column = col.Name()
			}
			resultMap[groupByVal.String()] = aggregate
		}

		for i, it := range columnIter {
			values, err := it.NextGroup()
			if err != nil {
				return err
			}

			for _, val := range values {
				if val.IsNull() {
					aggregate.Stats[i].Nulls++
				} else {
					aggregate.Stats[i].Values++
					aggregate.Stats[i].Size += len(val.Bytes())
				}
			}
		}
	}

	// convert result map to slice sorted by result map key
	resultKeys := make([]string, 0, len(resultMap))
	for key := range resultMap {
		resultKeys = append(resultKeys, key)
	}
	sort.Strings(resultKeys)

	c.result = make([]*Aggregate, 0, len(resultKeys))
	for _, key := range resultKeys {
		c.result = append(c.result, resultMap[key])
	}

	return nil
}
