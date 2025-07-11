package distributor

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	kitlog "github.com/go-kit/log"
	"github.com/gogo/status"
	"github.com/golang/protobuf/proto" // nolint: all  //ProtoReflect
	"github.com/grafana/dskit/flagext"
	dslog "github.com/grafana/dskit/log"
	"github.com/grafana/dskit/ring"
	ring_client "github.com/grafana/dskit/ring/client"
	"github.com/grafana/dskit/user"
	"github.com/grafana/tempo/modules/generator"
	"github.com/grafana/tempo/pkg/ingest"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"

	"github.com/grafana/tempo/modules/distributor/receiver"
	generator_client "github.com/grafana/tempo/modules/generator/client"
	ingester_client "github.com/grafana/tempo/modules/ingester/client"
	"github.com/grafana/tempo/modules/overrides"
	"github.com/grafana/tempo/pkg/tempopb"
	v1_common "github.com/grafana/tempo/pkg/tempopb/common/v1"
	v1_resource "github.com/grafana/tempo/pkg/tempopb/resource/v1"
	v1 "github.com/grafana/tempo/pkg/tempopb/trace/v1"
	"github.com/grafana/tempo/pkg/util"
	"github.com/grafana/tempo/pkg/util/test"
)

const (
	numIngesters       = 5
	noError            = tempopb.PushErrorReason_NO_ERROR
	maxLiveTraceError  = tempopb.PushErrorReason_MAX_LIVE_TRACES
	traceTooLargeError = tempopb.PushErrorReason_TRACE_TOO_LARGE
)

var ctx = user.InjectOrgID(context.Background(), "test")

func batchesToTraces(t *testing.T, batches []*v1.ResourceSpans) ptrace.Traces {
	t.Helper()

	trace := tempopb.Trace{ResourceSpans: batches}

	m, err := trace.Marshal()
	require.NoError(t, err)

	traces, err := (&ptrace.ProtoUnmarshaler{}).UnmarshalTraces(m)
	require.NoError(t, err)

	return traces
}

func TestRequestsByTraceID(t *testing.T) {
	traceIDA := []byte{0x0A, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F}
	traceIDB := []byte{0x0B, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F}

	// These 2 trace IDs are known to collide under fnv32
	collision1, _ := util.HexStringToTraceID("fd5980503add11f09f80f77608c1b2da")
	collision2, _ := util.HexStringToTraceID("091ea7803ade11f0998a055186ee1243")

	tests := []struct {
		name           string
		emptyTenant    bool
		batches        []*v1.ResourceSpans
		expectedKeys   []uint32
		expectedTraces []*tempopb.Trace
		expectedIDs    [][]byte
		expectedErr    error
		expectedStarts []uint32
		expectedEnds   []uint32
	}{
		{
			name: "empty",
			batches: []*v1.ResourceSpans{
				{},
				{},
			},
			expectedKeys:   []uint32{},
			expectedTraces: []*tempopb.Trace{},
			expectedIDs:    [][]byte{},
			expectedStarts: []uint32{},
			expectedEnds:   []uint32{},
		},
		{
			name: "bad trace id",
			batches: []*v1.ResourceSpans{
				{
					ScopeSpans: []*v1.ScopeSpans{
						{
							Spans: []*v1.Span{
								{
									TraceId: []byte{0x01},
								},
							},
						},
					},
				},
			},
			expectedErr: status.Errorf(codes.InvalidArgument, "trace ids must be 128 bit, received 8 bits"),
		},
		{
			name: "empty trace id",
			batches: []*v1.ResourceSpans{
				{
					ScopeSpans: []*v1.ScopeSpans{
						{
							Spans: []*v1.Span{
								{
									TraceId: []byte{},
								},
							},
						},
					},
				},
			},
			expectedErr: status.Errorf(codes.InvalidArgument, "trace ids must be 128 bit, received 0 bits"),
		},
		{
			name: "one span",
			batches: []*v1.ResourceSpans{
				{
					ScopeSpans: []*v1.ScopeSpans{
						{
							Spans: []*v1.Span{
								{
									TraceId:           traceIDA,
									StartTimeUnixNano: uint64(10 * time.Second),
									EndTimeUnixNano:   uint64(20 * time.Second),
								},
							},
						},
					},
				},
			},
			expectedKeys: []uint32{util.TokenFor(util.FakeTenantID, traceIDA)},
			expectedTraces: []*tempopb.Trace{
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							ScopeSpans: []*v1.ScopeSpans{
								{
									Spans: []*v1.Span{
										{
											TraceId:           traceIDA,
											StartTimeUnixNano: uint64(10 * time.Second),
											EndTimeUnixNano:   uint64(20 * time.Second),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedIDs: [][]byte{
				traceIDA,
			},
			expectedStarts: []uint32{10},
			expectedEnds:   []uint32{20},
		},
		{
			name: "two traces, one batch",
			batches: []*v1.ResourceSpans{
				{
					ScopeSpans: []*v1.ScopeSpans{
						{
							Spans: []*v1.Span{
								{
									TraceId:           traceIDA,
									StartTimeUnixNano: uint64(30 * time.Second),
									EndTimeUnixNano:   uint64(40 * time.Second),
								},
								{
									TraceId:           traceIDB,
									StartTimeUnixNano: uint64(50 * time.Second),
									EndTimeUnixNano:   uint64(60 * time.Second),
								},
							},
						},
					},
				},
			},
			expectedKeys: []uint32{util.TokenFor(util.FakeTenantID, traceIDA), util.TokenFor(util.FakeTenantID, traceIDB)},
			expectedTraces: []*tempopb.Trace{
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							ScopeSpans: []*v1.ScopeSpans{
								{
									Spans: []*v1.Span{
										{
											TraceId:           traceIDA,
											StartTimeUnixNano: uint64(30 * time.Second),
											EndTimeUnixNano:   uint64(40 * time.Second),
										},
									},
								},
							},
						},
					},
				},
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							ScopeSpans: []*v1.ScopeSpans{
								{
									Spans: []*v1.Span{
										{
											TraceId:           traceIDB,
											StartTimeUnixNano: uint64(50 * time.Second),
											EndTimeUnixNano:   uint64(60 * time.Second),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedIDs: [][]byte{
				traceIDA,
				traceIDB,
			},
			expectedStarts: []uint32{30, 50},
			expectedEnds:   []uint32{40, 60},
		},
		{
			name: "two traces, distinct batches",
			batches: []*v1.ResourceSpans{
				{
					Resource: &v1_resource.Resource{
						DroppedAttributesCount: 3,
					},
					ScopeSpans: []*v1.ScopeSpans{
						{
							Spans: []*v1.Span{
								{
									TraceId:           traceIDA,
									StartTimeUnixNano: uint64(30 * time.Second),
									EndTimeUnixNano:   uint64(40 * time.Second),
								},
							},
						},
					},
				},
				{
					Resource: &v1_resource.Resource{
						DroppedAttributesCount: 4,
					},
					ScopeSpans: []*v1.ScopeSpans{
						{
							Spans: []*v1.Span{
								{
									TraceId:           traceIDB,
									StartTimeUnixNano: uint64(50 * time.Second),
									EndTimeUnixNano:   uint64(60 * time.Second),
								},
							},
						},
					},
				},
			},
			expectedKeys: []uint32{util.TokenFor(util.FakeTenantID, traceIDA), util.TokenFor(util.FakeTenantID, traceIDB)},
			expectedTraces: []*tempopb.Trace{
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 3,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Spans: []*v1.Span{
										{
											TraceId:           traceIDA,
											StartTimeUnixNano: uint64(30 * time.Second),
											EndTimeUnixNano:   uint64(40 * time.Second),
										},
									},
								},
							},
						},
					},
				},
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 4,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Spans: []*v1.Span{
										{
											TraceId:           traceIDB,
											StartTimeUnixNano: uint64(50 * time.Second),
											EndTimeUnixNano:   uint64(60 * time.Second),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedIDs: [][]byte{
				traceIDA,
				traceIDB,
			},
			expectedStarts: []uint32{30, 50},
			expectedEnds:   []uint32{40, 60},
		},
		{
			name: "resource copied",
			batches: []*v1.ResourceSpans{
				{
					Resource: &v1_resource.Resource{
						DroppedAttributesCount: 1,
					},
					ScopeSpans: []*v1.ScopeSpans{
						{
							Spans: []*v1.Span{
								{
									TraceId:           traceIDA,
									StartTimeUnixNano: uint64(30 * time.Second),
									EndTimeUnixNano:   uint64(40 * time.Second),
								},
								{
									TraceId:           traceIDB,
									StartTimeUnixNano: uint64(50 * time.Second),
									EndTimeUnixNano:   uint64(60 * time.Second),
								},
							},
						},
					},
				},
			},
			expectedKeys: []uint32{util.TokenFor(util.FakeTenantID, traceIDA), util.TokenFor(util.FakeTenantID, traceIDB)},
			expectedTraces: []*tempopb.Trace{
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 1,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Spans: []*v1.Span{
										{
											TraceId:           traceIDA,
											StartTimeUnixNano: uint64(30 * time.Second),
											EndTimeUnixNano:   uint64(40 * time.Second),
										},
									},
								},
							},
						},
					},
				},
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 1,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Spans: []*v1.Span{
										{
											TraceId:           traceIDB,
											StartTimeUnixNano: uint64(50 * time.Second),
											EndTimeUnixNano:   uint64(60 * time.Second),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedIDs: [][]byte{
				traceIDA,
				traceIDB,
			},
			expectedStarts: []uint32{30, 50},
			expectedEnds:   []uint32{40, 60},
		},
		{
			name: "ils copied",
			batches: []*v1.ResourceSpans{
				{
					ScopeSpans: []*v1.ScopeSpans{
						{
							Scope: &v1_common.InstrumentationScope{
								Name: "test",
							},
							Spans: []*v1.Span{
								{
									TraceId:           traceIDA,
									StartTimeUnixNano: uint64(30 * time.Second),
									EndTimeUnixNano:   uint64(40 * time.Second),
								},
								{
									TraceId:           traceIDB,
									StartTimeUnixNano: uint64(50 * time.Second),
									EndTimeUnixNano:   uint64(60 * time.Second),
								},
							},
						},
					},
				},
			},
			expectedKeys: []uint32{util.TokenFor(util.FakeTenantID, traceIDA), util.TokenFor(util.FakeTenantID, traceIDB)},
			expectedTraces: []*tempopb.Trace{
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test",
									},
									Spans: []*v1.Span{
										{
											TraceId:           traceIDA,
											StartTimeUnixNano: uint64(30 * time.Second),
											EndTimeUnixNano:   uint64(40 * time.Second),
										},
									},
								},
							},
						},
					},
				},
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test",
									},
									Spans: []*v1.Span{
										{
											TraceId:           traceIDB,
											StartTimeUnixNano: uint64(50 * time.Second),
											EndTimeUnixNano:   uint64(60 * time.Second),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedIDs: [][]byte{
				traceIDA,
				traceIDB,
			},
			expectedStarts: []uint32{30, 50},
			expectedEnds:   []uint32{40, 60},
		},
		{
			name: "one trace",
			batches: []*v1.ResourceSpans{
				{
					Resource: &v1_resource.Resource{
						DroppedAttributesCount: 3,
					},
					ScopeSpans: []*v1.ScopeSpans{
						{
							Scope: &v1_common.InstrumentationScope{
								Name: "test",
							},
							Spans: []*v1.Span{
								{
									TraceId:           traceIDB,
									Name:              "spanA",
									StartTimeUnixNano: uint64(30 * time.Second),
									EndTimeUnixNano:   uint64(40 * time.Second),
								},
								{
									TraceId:           traceIDB,
									Name:              "spanB",
									StartTimeUnixNano: uint64(50 * time.Second),
									EndTimeUnixNano:   uint64(60 * time.Second),
								},
							},
						},
					},
				},
			},
			expectedKeys: []uint32{util.TokenFor(util.FakeTenantID, traceIDB)},
			expectedTraces: []*tempopb.Trace{
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 3,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test",
									},
									Spans: []*v1.Span{
										{
											TraceId:           traceIDB,
											Name:              "spanA",
											StartTimeUnixNano: uint64(30 * time.Second),
											EndTimeUnixNano:   uint64(40 * time.Second),
										},
										{
											TraceId:           traceIDB,
											Name:              "spanB",
											StartTimeUnixNano: uint64(50 * time.Second),
											EndTimeUnixNano:   uint64(60 * time.Second),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedIDs: [][]byte{
				traceIDB,
			},
			expectedStarts: []uint32{30},
			expectedEnds:   []uint32{60},
		},
		{
			name: "two traces - two batches - don't combine across batches",
			batches: []*v1.ResourceSpans{
				{
					Resource: &v1_resource.Resource{
						DroppedAttributesCount: 3,
					},
					ScopeSpans: []*v1.ScopeSpans{
						{
							Scope: &v1_common.InstrumentationScope{
								Name: "test",
							},
							Spans: []*v1.Span{
								{
									TraceId:           traceIDB,
									Name:              "spanA",
									StartTimeUnixNano: uint64(30 * time.Second),
									EndTimeUnixNano:   uint64(40 * time.Second),
								},
								{
									TraceId:           traceIDB,
									Name:              "spanC",
									StartTimeUnixNano: uint64(20 * time.Second),
									EndTimeUnixNano:   uint64(50 * time.Second),
								},
								{
									TraceId:           traceIDA,
									Name:              "spanE",
									StartTimeUnixNano: uint64(70 * time.Second),
									EndTimeUnixNano:   uint64(80 * time.Second),
								},
							},
						},
					},
				},
				{
					Resource: &v1_resource.Resource{
						DroppedAttributesCount: 4,
					},
					ScopeSpans: []*v1.ScopeSpans{
						{
							Scope: &v1_common.InstrumentationScope{
								Name: "test2",
							},
							Spans: []*v1.Span{
								{
									TraceId:           traceIDB,
									Name:              "spanB",
									StartTimeUnixNano: uint64(10 * time.Second),
									EndTimeUnixNano:   uint64(30 * time.Second),
								},
								{
									TraceId:           traceIDA,
									Name:              "spanD",
									StartTimeUnixNano: uint64(60 * time.Second),
									EndTimeUnixNano:   uint64(80 * time.Second),
								},
							},
						},
					},
				},
			},
			expectedKeys: []uint32{
				util.TokenFor(util.FakeTenantID, traceIDB),
				util.TokenFor(util.FakeTenantID, traceIDA),
			},
			expectedTraces: []*tempopb.Trace{
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 3,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test",
									},
									Spans: []*v1.Span{
										{
											TraceId:           traceIDB,
											Name:              "spanA",
											StartTimeUnixNano: uint64(30 * time.Second),
											EndTimeUnixNano:   uint64(40 * time.Second),
										},
										{
											TraceId:           traceIDB,
											Name:              "spanC",
											StartTimeUnixNano: uint64(20 * time.Second),
											EndTimeUnixNano:   uint64(50 * time.Second),
										},
									},
								},
							},
						},
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 4,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test2",
									},
									Spans: []*v1.Span{
										{
											TraceId:           traceIDB,
											Name:              "spanB",
											StartTimeUnixNano: uint64(10 * time.Second),
											EndTimeUnixNano:   uint64(30 * time.Second),
										},
									},
								},
							},
						},
					},
				},
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 3,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test",
									},
									Spans: []*v1.Span{
										{
											TraceId:           traceIDA,
											Name:              "spanE",
											StartTimeUnixNano: uint64(70 * time.Second),
											EndTimeUnixNano:   uint64(80 * time.Second),
										},
									},
								},
							},
						},
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 4,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test2",
									},
									Spans: []*v1.Span{
										{
											TraceId:           traceIDA,
											Name:              "spanD",
											StartTimeUnixNano: uint64(60 * time.Second),
											EndTimeUnixNano:   uint64(80 * time.Second),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedIDs: [][]byte{
				traceIDB,
				traceIDA,
			},
			expectedStarts: []uint32{10, 60},
			expectedEnds:   []uint32{50, 80},
		},
		{
			// These 2 trace IDs are known to collide under fnv32
			name:        "known collisions",
			emptyTenant: true,
			batches: []*v1.ResourceSpans{
				{
					Resource: &v1_resource.Resource{
						DroppedAttributesCount: 3,
					},
					ScopeSpans: []*v1.ScopeSpans{
						{
							Scope: &v1_common.InstrumentationScope{
								Name: "test",
							},
							Spans: []*v1.Span{
								{
									TraceId:           collision2,
									Name:              "spanA",
									StartTimeUnixNano: uint64(30 * time.Second),
									EndTimeUnixNano:   uint64(40 * time.Second),
								},
								{
									TraceId:           collision2,
									Name:              "spanC",
									StartTimeUnixNano: uint64(20 * time.Second),
									EndTimeUnixNano:   uint64(50 * time.Second),
								},
								{
									TraceId:           collision1,
									Name:              "spanE",
									StartTimeUnixNano: uint64(70 * time.Second),
									EndTimeUnixNano:   uint64(80 * time.Second),
								},
							},
						},
					},
				},
				{
					Resource: &v1_resource.Resource{
						DroppedAttributesCount: 4,
					},
					ScopeSpans: []*v1.ScopeSpans{
						{
							Scope: &v1_common.InstrumentationScope{
								Name: "test2",
							},
							Spans: []*v1.Span{
								{
									TraceId:           collision2,
									Name:              "spanB",
									StartTimeUnixNano: uint64(10 * time.Second),
									EndTimeUnixNano:   uint64(30 * time.Second),
								},
								{
									TraceId:           collision1,
									Name:              "spanD",
									StartTimeUnixNano: uint64(60 * time.Second),
									EndTimeUnixNano:   uint64(80 * time.Second),
								},
							},
						},
					},
				},
			},
			expectedKeys: []uint32{
				util.TokenFor("", collision1),
				util.TokenFor("", collision2),
			},
			expectedTraces: []*tempopb.Trace{
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 3,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test",
									},
									Spans: []*v1.Span{
										{
											TraceId:           collision1,
											Name:              "spanE",
											StartTimeUnixNano: uint64(70 * time.Second),
											EndTimeUnixNano:   uint64(80 * time.Second),
										},
									},
								},
							},
						},
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 4,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test2",
									},
									Spans: []*v1.Span{
										{
											TraceId:           collision1,
											Name:              "spanD",
											StartTimeUnixNano: uint64(60 * time.Second),
											EndTimeUnixNano:   uint64(80 * time.Second),
										},
									},
								},
							},
						},
					},
				},
				{
					ResourceSpans: []*v1.ResourceSpans{
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 3,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test",
									},
									Spans: []*v1.Span{
										{
											TraceId:           collision2,
											Name:              "spanA",
											StartTimeUnixNano: uint64(30 * time.Second),
											EndTimeUnixNano:   uint64(40 * time.Second),
										},
										{
											TraceId:           collision2,
											Name:              "spanC",
											StartTimeUnixNano: uint64(20 * time.Second),
											EndTimeUnixNano:   uint64(50 * time.Second),
										},
									},
								},
							},
						},
						{
							Resource: &v1_resource.Resource{
								DroppedAttributesCount: 4,
							},
							ScopeSpans: []*v1.ScopeSpans{
								{
									Scope: &v1_common.InstrumentationScope{
										Name: "test2",
									},
									Spans: []*v1.Span{
										{
											TraceId:           collision2,
											Name:              "spanB",
											StartTimeUnixNano: uint64(10 * time.Second),
											EndTimeUnixNano:   uint64(30 * time.Second),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedIDs: [][]byte{
				collision1,
				collision2,
			},
			expectedStarts: []uint32{60, 10},
			expectedEnds:   []uint32{80, 50},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenant := util.FakeTenantID
			if tt.emptyTenant {
				tenant = ""
			}
			ringTokens, rebatchedTraces, _, err := requestsByTraceID(tt.batches, tenant, 1, 1000)
			require.Equal(t, len(ringTokens), len(rebatchedTraces))

			for i, expectedID := range tt.expectedIDs {
				foundIndex := -1
				for j, tr := range rebatchedTraces {
					if bytes.Equal(expectedID, tr.id) {
						foundIndex = j
						break
					}
				}
				require.NotEqual(t, -1, foundIndex, "expected key %d not found", foundIndex)

				// now confirm that the request at this position is the expected one
				require.Equal(t, tt.expectedIDs[i], rebatchedTraces[foundIndex].id)
				require.Equal(t, tt.expectedTraces[i], rebatchedTraces[foundIndex].trace)
				require.Equal(t, tt.expectedStarts[i], rebatchedTraces[foundIndex].start)
				require.Equal(t, tt.expectedEnds[i], rebatchedTraces[foundIndex].end)
			}

			require.Equal(t, tt.expectedErr, err)
		})
	}
}

func TestProcessAttributes(t *testing.T) {
	spanCount := 10
	batchCount := 3
	trace := test.MakeTraceWithSpanCount(batchCount, spanCount, []byte{0x0A, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F})

	maxAttrByte := 1000
	longString := strings.Repeat("t", 1100)

	// add long attributes to the resource level
	trace.ResourceSpans[0].Resource.Attributes = append(trace.ResourceSpans[0].Resource.Attributes,
		test.MakeAttribute("long value", longString),
	)
	trace.ResourceSpans[0].Resource.Attributes = append(trace.ResourceSpans[0].Resource.Attributes,
		test.MakeAttribute(longString, "long key"),
	)

	// add long attributes to the span level
	trace.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes = append(trace.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes,
		test.MakeAttribute("long value", longString),
	)
	trace.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes = append(trace.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes,
		test.MakeAttribute(longString, "long key"),
	)

	// add long attributes to the event level
	trace.ResourceSpans[0].ScopeSpans[0].Spans[0].Events = append(trace.ResourceSpans[0].ScopeSpans[0].Spans[0].Events,
		&v1.Span_Event{
			TimeUnixNano: 0,
			Attributes: []*v1_common.KeyValue{
				test.MakeAttribute("long value", longString),
				test.MakeAttribute(longString, "long key"),
			},
		},
	)

	// add long attributes to the link level
	trace.ResourceSpans[0].ScopeSpans[0].Spans[0].Links = append(trace.ResourceSpans[0].ScopeSpans[0].Spans[0].Links,
		&v1.Span_Link{
			TraceId: []byte{0x0A, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F},
			SpanId:  []byte{0x0A, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F},
			Attributes: []*v1_common.KeyValue{
				test.MakeAttribute("long value", longString),
				test.MakeAttribute(longString, "long key"),
			},
		},
	)

	// add long attributes to scope level
	trace.ResourceSpans[0].ScopeSpans[0].Scope = &v1_common.InstrumentationScope{
		Name:    "scope scope",
		Version: "1.0",
		Attributes: []*v1_common.KeyValue{
			test.MakeAttribute("long value", longString),
			test.MakeAttribute(longString, "long key"),
		},
	}

	_, rebatchedTrace, truncatedCount, _ := requestsByTraceID(trace.ResourceSpans, "test", spanCount*batchCount, maxAttrByte)
	// 2 at resource level, 2 at span level, 2 at event level, 2 at link level, 2 at scope level
	assert.Equal(t, 10, truncatedCount)
	for _, rT := range rebatchedTrace {
		for _, resource := range rT.trace.ResourceSpans {
			// find large resource attributes
			for _, attr := range resource.Resource.Attributes {
				if attr.Key == "long value" {
					assert.Equal(t, longString[:maxAttrByte], attr.Value.GetStringValue())
				}
				if attr.Value.GetStringValue() == "long key" {
					assert.Equal(t, longString[:maxAttrByte], attr.Key)
				}
			}
			// find large span attributes
			for _, scope := range resource.ScopeSpans {
				for _, attr := range scope.Scope.Attributes {
					if attr.Key == "long value" {
						assert.Equal(t, longString[:maxAttrByte], attr.Value.GetStringValue())
					}
					if attr.Value.GetStringValue() == "long key" {
						assert.Equal(t, longString[:maxAttrByte], attr.Key)
					}
				}

				for _, span := range scope.Spans {
					for _, attr := range span.Attributes {
						if attr.Key == "long value" {
							assert.Equal(t, longString[:maxAttrByte], attr.Value.GetStringValue())
						}
						if attr.Value.GetStringValue() == "long key" {
							assert.Equal(t, longString[:maxAttrByte], attr.Key)
						}
					}
					// events
					for _, event := range span.Events {
						for _, attr := range event.Attributes {
							if attr.Key == "long value" {
								assert.Equal(t, longString[:maxAttrByte], attr.Value.GetStringValue())
							}
							if attr.Value.GetStringValue() == "long key" {
								assert.Equal(t, longString[:maxAttrByte], attr.Key)
							}
						}
					}

					// links
					for _, link := range span.Links {
						for _, attr := range link.Attributes {
							if attr.Key == "long value" {
								assert.Equal(t, longString[:maxAttrByte], attr.Value.GetStringValue())
							}
							if attr.Value.GetStringValue() == "long key" {
								assert.Equal(t, longString[:maxAttrByte], attr.Key)
							}
						}
					}
				}
			}

		}
	}
}

func BenchmarkTestsByRequestID(b *testing.B) {
	spansPer := 5000
	batches := 100
	traces := []*tempopb.Trace{
		test.MakeTraceWithSpanCount(batches, spansPer, []byte{0x0A, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F}),
		test.MakeTraceWithSpanCount(batches, spansPer, []byte{0x0B, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F}),
		test.MakeTraceWithSpanCount(batches, spansPer, []byte{0x0C, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F}),
		test.MakeTraceWithSpanCount(batches, spansPer, []byte{0x0D, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F}),
	}
	ils := make([][]*v1.ScopeSpans, batches)

	for i := 0; i < batches; i++ {
		for _, t := range traces {
			ils[i] = append(ils[i], t.ResourceSpans[i].ScopeSpans...)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for _, blerg := range ils {
			_, _, _, err := requestsByTraceID([]*v1.ResourceSpans{
				{
					ScopeSpans: blerg,
				},
			}, "test", spansPer*len(traces), 5)
			require.NoError(b, err)
		}
	}
}

func TestDistributor(t *testing.T) {
	for i, tc := range []struct {
		lines            int
		expectedResponse *tempopb.PushResponse
		expectedError    error
	}{
		{
			lines:            10,
			expectedResponse: nil,
		},
		{
			lines:            100,
			expectedResponse: nil,
		},
	} {
		t.Run(fmt.Sprintf("[%d](samples=%v)", i, tc.lines), func(t *testing.T) {
			limits := overrides.Config{}
			limits.RegisterFlagsAndApplyDefaults(&flag.FlagSet{})

			// todo:  test limits
			d, _ := prepare(t, limits, nil)

			b := test.MakeBatch(tc.lines, []byte{})
			traces := batchesToTraces(t, []*v1.ResourceSpans{b})
			response, err := d.PushTraces(ctx, traces)

			assert.True(t, proto.Equal(tc.expectedResponse, response))
			assert.Equal(t, tc.expectedError, err)
		})
	}
}

func TestLogReceivedSpans(t *testing.T) {
	for i, tc := range []struct {
		LogReceivedSpansEnabled bool
		filterByStatusError     bool
		includeAllAttributes    bool
		batches                 []*v1.ResourceSpans
		expectedLogsSpan        []testLogSpan
	}{
		{
			LogReceivedSpansEnabled: false,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span", nil)),
				}),
			},
			expectedLogsSpan: []testLogSpan{},
		},
		{
			LogReceivedSpansEnabled: true,
			filterByStatusError:     false,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test-service", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span1", nil),
						makeSpan("e3210a2b38097332d1fe43083ea93d29", "6c21c48da4dbd1a7", "Test Span2", nil)),
					makeScope(
						makeSpan("bb42ec04df789ff04b10ea5274491685", "1b3a296034f4031e", "Test Span3", nil)),
				}),
				makeResourceSpans("test-service2", []*v1.ScopeSpans{
					makeScope(
						makeSpan("b1c792dea27d511c145df8402bdd793a", "56afb9fe18b6c2d6", "Test Span", nil)),
				}),
			},
			expectedLogsSpan: []testLogSpan{
				{
					Msg:     "received",
					Level:   "info",
					TraceID: "0a0102030405060708090a0b0c0d0e0f",
					SpanID:  "dad44adc9a83b370",
				},
				{
					Msg:     "received",
					Level:   "info",
					TraceID: "e3210a2b38097332d1fe43083ea93d29",
					SpanID:  "6c21c48da4dbd1a7",
				},
				{
					Msg:     "received",
					Level:   "info",
					TraceID: "bb42ec04df789ff04b10ea5274491685",
					SpanID:  "1b3a296034f4031e",
				},
				{
					Msg:     "received",
					Level:   "info",
					TraceID: "b1c792dea27d511c145df8402bdd793a",
					SpanID:  "56afb9fe18b6c2d6",
				},
			},
		},
		{
			LogReceivedSpansEnabled: true,
			filterByStatusError:     true,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test-service", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span1", nil),
						makeSpan("e3210a2b38097332d1fe43083ea93d29", "6c21c48da4dbd1a7", "Test Span2", &v1.Status{Code: v1.Status_STATUS_CODE_ERROR})),
					makeScope(
						makeSpan("bb42ec04df789ff04b10ea5274491685", "1b3a296034f4031e", "Test Span3", nil)),
				}),
				makeResourceSpans("test-service2", []*v1.ScopeSpans{
					makeScope(
						makeSpan("b1c792dea27d511c145df8402bdd793a", "56afb9fe18b6c2d6", "Test Span", &v1.Status{Code: v1.Status_STATUS_CODE_ERROR})),
				}),
			},
			expectedLogsSpan: []testLogSpan{
				{
					Msg:     "received",
					Level:   "info",
					TraceID: "e3210a2b38097332d1fe43083ea93d29",
					SpanID:  "6c21c48da4dbd1a7",
				},
				{
					Msg:     "received",
					Level:   "info",
					TraceID: "b1c792dea27d511c145df8402bdd793a",
					SpanID:  "56afb9fe18b6c2d6",
				},
			},
		},
		{
			LogReceivedSpansEnabled: true,
			filterByStatusError:     true,
			includeAllAttributes:    true,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test-service", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span1", nil,
							makeAttribute("tag1", "value1")),
						makeSpan("e3210a2b38097332d1fe43083ea93d29", "6c21c48da4dbd1a7", "Test Span2", &v1.Status{Code: v1.Status_STATUS_CODE_ERROR},
							makeAttribute("tag1", "value1"),
							makeAttribute("tag2", "value2"))),
					makeScope(
						makeSpan("bb42ec04df789ff04b10ea5274491685", "1b3a296034f4031e", "Test Span3", nil)),
				}, makeAttribute("resource_attribute1", "value1")),
				makeResourceSpans("test-service2", []*v1.ScopeSpans{
					makeScope(
						makeSpan("b1c792dea27d511c145df8402bdd793a", "56afb9fe18b6c2d6", "Test Span", &v1.Status{Code: v1.Status_STATUS_CODE_ERROR})),
				}, makeAttribute("resource_attribute2", "value2")),
			},
			expectedLogsSpan: []testLogSpan{
				{
					Name:               "Test Span2",
					Msg:                "received",
					Level:              "info",
					TraceID:            "e3210a2b38097332d1fe43083ea93d29",
					SpanID:             "6c21c48da4dbd1a7",
					SpanServiceName:    "test-service",
					SpanStatus:         "STATUS_CODE_ERROR",
					SpanKind:           "SPAN_KIND_SERVER",
					SpanTag1:           "value1",
					SpanTag2:           "value2",
					ResourceAttribute1: "value1",
				},
				{
					Name:               "Test Span",
					Msg:                "received",
					Level:              "info",
					TraceID:            "b1c792dea27d511c145df8402bdd793a",
					SpanID:             "56afb9fe18b6c2d6",
					SpanServiceName:    "test-service2",
					SpanStatus:         "STATUS_CODE_ERROR",
					SpanKind:           "SPAN_KIND_SERVER",
					ResourceAttribute2: "value2",
				},
			},
		},
		{
			LogReceivedSpansEnabled: true,
			filterByStatusError:     false,
			includeAllAttributes:    true,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test-service", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span", nil, makeAttribute("tag1", "value1"))),
				}),
			},
			expectedLogsSpan: []testLogSpan{
				{
					Name:            "Test Span",
					Msg:             "received",
					Level:           "info",
					TraceID:         "0a0102030405060708090a0b0c0d0e0f",
					SpanID:          "dad44adc9a83b370",
					SpanServiceName: "test-service",
					SpanStatus:      "STATUS_CODE_OK",
					SpanKind:        "SPAN_KIND_SERVER",
					SpanTag1:        "value1",
				},
			},
		},
	} {
		t.Run(fmt.Sprintf("[%d] TestLogReceivedSpans LogReceivedSpansEnabled=%v filterByStatusError=%v includeAllAttributes=%v", i, tc.LogReceivedSpansEnabled, tc.filterByStatusError, tc.includeAllAttributes), func(t *testing.T) {
			limits := overrides.Config{}
			limits.RegisterFlagsAndApplyDefaults(&flag.FlagSet{})

			buf := &bytes.Buffer{}
			logger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(buf))

			d, _ := prepare(t, limits, logger)
			d.cfg.LogReceivedSpans = LogSpansConfig{
				Enabled:              tc.LogReceivedSpansEnabled,
				FilterByStatusError:  tc.filterByStatusError,
				IncludeAllAttributes: tc.includeAllAttributes,
			}

			traces := batchesToTraces(t, tc.batches)
			_, err := d.PushTraces(ctx, traces)
			if err != nil {
				t.Fatal(err)
			}

			assert.ElementsMatch(t, tc.expectedLogsSpan, actualLogSpan(t, buf))
		})
	}
}

func TestLogDiscardedSpansWhenContextCancelled(t *testing.T) {
	for i, tc := range []struct {
		LogDiscardedSpansEnabled bool
		filterByStatusError      bool
		includeAllAttributes     bool
		batches                  []*v1.ResourceSpans
		expectedLogsSpan         []testLogSpan
	}{
		{
			LogDiscardedSpansEnabled: false,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span", nil)),
				}),
			},
			expectedLogsSpan: []testLogSpan{},
		},
		{
			LogDiscardedSpansEnabled: true,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span", nil)),
				}),
			},
			expectedLogsSpan: []testLogSpan{
				{
					Msg:     "discarded",
					Level:   "info",
					Tenant:  "test",
					TraceID: "0a0102030405060708090a0b0c0d0e0f",
					SpanID:  "dad44adc9a83b370",
				},
			},
		},
		{
			LogDiscardedSpansEnabled: true,
			includeAllAttributes:     true,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test-service2", []*v1.ScopeSpans{
					makeScope(
						makeSpan("b1c792dea27d511c145df8402bdd793a", "56afb9fe18b6c2d6", "Test Span", &v1.Status{Code: v1.Status_STATUS_CODE_ERROR})),
				}, makeAttribute("resource_attribute2", "value2")),
			},
			expectedLogsSpan: []testLogSpan{
				{
					Name:               "Test Span",
					Msg:                "discarded",
					Level:              "info",
					Tenant:             "test",
					TraceID:            "b1c792dea27d511c145df8402bdd793a",
					SpanID:             "56afb9fe18b6c2d6",
					SpanServiceName:    "test-service2",
					SpanStatus:         "STATUS_CODE_ERROR",
					SpanKind:           "SPAN_KIND_SERVER",
					ResourceAttribute2: "value2",
				},
			},
		},
		{
			LogDiscardedSpansEnabled: true,
			filterByStatusError:      true,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test-service", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span1", nil),
						makeSpan("e3210a2b38097332d1fe43083ea93d29", "6c21c48da4dbd1a7", "Test Span2", &v1.Status{Code: v1.Status_STATUS_CODE_ERROR})),
					makeScope(
						makeSpan("bb42ec04df789ff04b10ea5274491685", "1b3a296034f4031e", "Test Span3", nil)),
				}),
				makeResourceSpans("test-service2", []*v1.ScopeSpans{
					makeScope(
						makeSpan("b1c792dea27d511c145df8402bdd793a", "56afb9fe18b6c2d6", "Test Span", &v1.Status{Code: v1.Status_STATUS_CODE_ERROR})),
				}),
			},
			expectedLogsSpan: []testLogSpan{
				{
					Msg:     "discarded",
					Level:   "info",
					Tenant:  "test",
					TraceID: "e3210a2b38097332d1fe43083ea93d29",
					SpanID:  "6c21c48da4dbd1a7",
				},
				{
					Msg:     "discarded",
					Level:   "info",
					Tenant:  "test",
					TraceID: "b1c792dea27d511c145df8402bdd793a",
					SpanID:  "56afb9fe18b6c2d6",
				},
			},
		},
	} {
		t.Run(fmt.Sprintf("[%d] TestLogDiscardedSpansWhenContextCancelled LogDiscardedSpansEnabled=%v filterByStatusError=%v includeAllAttributes=%v", i, tc.LogDiscardedSpansEnabled, tc.filterByStatusError, tc.includeAllAttributes), func(t *testing.T) {
			limits := overrides.Config{}
			limits.RegisterFlagsAndApplyDefaults(&flag.FlagSet{})

			buf := &bytes.Buffer{}
			logger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(buf))

			d, _ := prepare(t, limits, logger)
			d.cfg.LogDiscardedSpans = LogSpansConfig{
				Enabled:              tc.LogDiscardedSpansEnabled,
				FilterByStatusError:  tc.filterByStatusError,
				IncludeAllAttributes: tc.includeAllAttributes,
			}

			traces := batchesToTraces(t, tc.batches)
			ctx, cancelFunc := context.WithCancelCause(ctx)
			cause := errors.New("test cause")
			cancelFunc(cause) // cancel to force all spans to be discarded

			_, err := d.PushTraces(ctx, traces)
			assert.Equal(t, cause, err)

			assert.ElementsMatch(t, tc.expectedLogsSpan, actualLogSpan(t, buf))
		})
	}
}

func TestLogDiscardedSpansWhenPushToIngesterFails(t *testing.T) {
	for i, tc := range []struct {
		LogDiscardedSpansEnabled bool
		filterByStatusError      bool
		includeAllAttributes     bool
		batches                  []*v1.ResourceSpans
		expectedLogsSpan         []testLogSpan
		pushErrorByTrace         []tempopb.PushErrorReason
	}{
		{
			LogDiscardedSpansEnabled: false,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span", nil)),
				}),
			},
			pushErrorByTrace: []tempopb.PushErrorReason{traceTooLargeError},
			expectedLogsSpan: []testLogSpan{},
		},
		{
			LogDiscardedSpansEnabled: true,
			batches: []*v1.ResourceSpans{
				makeResourceSpans("test", []*v1.ScopeSpans{
					makeScope(
						makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span", nil)),
				}),
			},
			pushErrorByTrace: []tempopb.PushErrorReason{traceTooLargeError},
			expectedLogsSpan: []testLogSpan{
				{
					Msg:             "discarded",
					Level:           "info",
					PushErrorReason: "TRACE_TOO_LARGE",
					Tenant:          "test",
					TraceID:         "0a0102030405060708090a0b0c0d0e0f",
					SpanID:          "dad44adc9a83b370",
				},
			},
		},
	} {
		t.Run(fmt.Sprintf("[%d] TestLogDiscardedSpansWhenPushToIngesterFails LogDiscardedSpansEnabled=%v filterByStatusError=%v includeAllAttributes=%v", i, tc.LogDiscardedSpansEnabled, tc.filterByStatusError, tc.includeAllAttributes), func(t *testing.T) {
			limits := overrides.Config{}
			limits.RegisterFlagsAndApplyDefaults(&flag.FlagSet{})

			buf := &bytes.Buffer{}
			logger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(buf))

			d, ingesters := prepare(t, limits, logger)
			d.cfg.LogDiscardedSpans = LogSpansConfig{
				Enabled:              tc.LogDiscardedSpansEnabled,
				FilterByStatusError:  tc.filterByStatusError,
				IncludeAllAttributes: tc.includeAllAttributes,
			}

			// mock ingester errors
			for ingester := range maps.Values(ingesters) {
				ingester.pushBytesV2 = func(_ context.Context, _ *tempopb.PushBytesRequest, _ ...grpc.CallOption) (*tempopb.PushResponse, error) {
					return &tempopb.PushResponse{
						ErrorsByTrace: tc.pushErrorByTrace,
					}, nil
				}
			}

			traces := batchesToTraces(t, tc.batches)

			_, err := d.PushTraces(ctx, traces)
			if err != nil {
				t.Fatal(err)
			}
			assert.ElementsMatch(t, tc.expectedLogsSpan, actualLogSpan(t, buf))
		})
	}
}

func actualLogSpan(t *testing.T, buf *bytes.Buffer) []testLogSpan {
	bufJSON := "[" + strings.TrimRight(strings.ReplaceAll(buf.String(), "\n", ","), ",") + "]"
	var actualLogsSpan []testLogSpan
	err := json.Unmarshal([]byte(bufJSON), &actualLogsSpan)
	if err != nil {
		t.Fatal(err)
	}
	return actualLogsSpan
}

func TestRateLimitRespected(t *testing.T) {
	// prepare test data
	overridesConfig := overrides.Config{
		Defaults: overrides.Overrides{
			Ingestion: overrides.IngestionOverrides{
				RateStrategy:   overrides.LocalIngestionRateStrategy,
				RateLimitBytes: 400,
				BurstSizeBytes: 200,
			},
		},
	}
	buf := &bytes.Buffer{}
	logger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(buf))
	d, _ := prepare(t, overridesConfig, logger)
	batches := []*v1.ResourceSpans{
		makeResourceSpans("test-service", []*v1.ScopeSpans{
			makeScope(
				makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span1", nil,
					makeAttribute("tag1", "value1")),
				makeSpan("e3210a2b38097332d1fe43083ea93d29", "6c21c48da4dbd1a7", "Test Span2", &v1.Status{Code: v1.Status_STATUS_CODE_ERROR},
					makeAttribute("tag1", "value1"),
					makeAttribute("tag2", "value2"))),
			makeScope(
				makeSpan("bb42ec04df789ff04b10ea5274491685", "1b3a296034f4031e", "Test Span3", nil)),
		}, makeAttribute("resource_attribute1", "value1")),
		makeResourceSpans("test-service2", []*v1.ScopeSpans{
			makeScope(
				makeSpan("b1c792dea27d511c145df8402bdd793a", "56afb9fe18b6c2d6", "Test Span", &v1.Status{Code: v1.Status_STATUS_CODE_ERROR})),
		}, makeAttribute("resource_attribute2", "value2")),
	}
	traces := batchesToTraces(t, batches)

	// invoke unit
	_, err := d.PushTraces(ctx, traces)

	// validations
	if err == nil {
		t.Fatal("Expected error")
	}
	status, ok := status.FromError(err)
	assert.True(t, ok)
	assert.True(t, status.Code() == codes.ResourceExhausted, "Wrong status code")
}

func TestDiscardCountReplicationFactor(t *testing.T) {
	tt := []struct {
		name                                string
		pushErrorByTrace                    [][]tempopb.PushErrorReason
		replicationFactor                   int
		expectedLiveTracesDiscardedCount    int
		expectedTraceTooLargeDiscardedCount int
	}{
		// trace sizes
		// trace[0] = 5 spans
		// trace[1] = 10 spans
		// trace[2] = 15 spans
		{
			name:                                "no errors, minimum responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, noError, noError}, {noError, noError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 0,
		},
		{
			name:                                "no error, max responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, noError, noError}, {noError, noError, noError}, {noError, noError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 0,
		},
		{
			name:                                "one mlt error, minimum responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{maxLiveTraceError, noError, noError}, {noError, noError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    5,
			expectedTraceTooLargeDiscardedCount: 0,
		},
		{
			name:                                "one mlt error, max responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{maxLiveTraceError, noError, noError}, {noError, noError, noError}, {noError, noError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 0,
		},
		{
			name:                                "one ttl error, minimum responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, traceTooLargeError, noError}, {noError, noError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 10,
		},
		{
			name:                                "one ttl error, max responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, traceTooLargeError, noError}, {noError, noError, noError}, {noError, noError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 0,
		},
		{
			name:                                "two mlt errors, minimum responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{maxLiveTraceError, noError, noError}, {maxLiveTraceError, noError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    5,
			expectedTraceTooLargeDiscardedCount: 0,
		},
		{
			name:                                "two ttl errors, max responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, traceTooLargeError, noError}, {noError, traceTooLargeError, noError}, {noError, noError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 10,
		},
		{
			name:                                "three ttl errors, max responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, traceTooLargeError, noError}, {noError, traceTooLargeError, noError}, {noError, traceTooLargeError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 10,
		},
		{
			name:                                "three mix errors, max responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, traceTooLargeError, noError}, {noError, maxLiveTraceError, noError}, {noError, traceTooLargeError, noError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 10,
		},
		{
			name:                                "three mix trace errors, max responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, traceTooLargeError, noError}, {noError, noError, traceTooLargeError}, {noError, maxLiveTraceError, traceTooLargeError}},
			replicationFactor:                   3,
			expectedLiveTracesDiscardedCount:    10,
			expectedTraceTooLargeDiscardedCount: 15,
		},
		{
			name:                                "one ttl error rep factor 5 min (3) response",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, traceTooLargeError, noError}, {noError, noError, noError}, {noError, noError, noError}},
			replicationFactor:                   5,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 10,
		},
		{
			name:                                "one error rep factor 5 with 4 responses",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, traceTooLargeError, noError}, {noError, noError, noError}, {noError, noError, noError}, {noError, noError, noError}},
			replicationFactor:                   5,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 0,
		},
		{
			name:                                "replication factor 1",
			pushErrorByTrace:                    [][]tempopb.PushErrorReason{{noError, traceTooLargeError, noError}},
			replicationFactor:                   1,
			expectedLiveTracesDiscardedCount:    0,
			expectedTraceTooLargeDiscardedCount: 10,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			traceByID := make([]*rebatchedTrace, 3)
			// batch with 3 traces
			traceByID[0] = &rebatchedTrace{
				spanCount: 5,
			}

			traceByID[1] = &rebatchedTrace{
				spanCount: 15,
			}

			traceByID[2] = &rebatchedTrace{
				spanCount: 10,
			}

			keys := []int{0, 2, 1}

			numSuccessByTraceIndex := make([]int, len(traceByID))
			lastErrorReasonByTraceIndex := make([]tempopb.PushErrorReason, len(traceByID))

			for _, ErrorByTrace := range tc.pushErrorByTrace {
				for ringIndex, err := range ErrorByTrace {
					// translate
					traceIndex := keys[ringIndex]

					currentNumSuccess := numSuccessByTraceIndex[traceIndex]
					if err == tempopb.PushErrorReason_NO_ERROR {
						numSuccessByTraceIndex[traceIndex] = currentNumSuccess + 1
					} else {
						lastErrorReasonByTraceIndex[traceIndex] = err
					}
				}
			}

			liveTraceDiscardedCount, traceTooLongDiscardedCount, _ := countDiscardedSpans(numSuccessByTraceIndex, lastErrorReasonByTraceIndex, traceByID, tc.replicationFactor)

			require.Equal(t, tc.expectedLiveTracesDiscardedCount, liveTraceDiscardedCount)
			require.Equal(t, tc.expectedTraceTooLargeDiscardedCount, traceTooLongDiscardedCount)
		})
	}
}

func TestProcessIngesterPushByteResponse(t *testing.T) {
	// batch has 5 traces [0, 1, 2, 3, 4, 5]
	numOfTraces := 5
	tt := []struct {
		name                   string
		pushErrorByTrace       []tempopb.PushErrorReason
		indexes                []int
		expectedSuccessIndex   []int
		expectedLastErrorIndex []tempopb.PushErrorReason
	}{
		{
			name:                   "explicit no errors, first three traces",
			pushErrorByTrace:       []tempopb.PushErrorReason{noError, noError, noError},
			indexes:                []int{0, 1, 2},
			expectedSuccessIndex:   []int{1, 1, 1, 0, 0},
			expectedLastErrorIndex: make([]tempopb.PushErrorReason, numOfTraces),
		},
		{
			name:                   "no errors, no ErrorsByTrace value",
			pushErrorByTrace:       []tempopb.PushErrorReason{},
			indexes:                []int{1, 2, 3},
			expectedSuccessIndex:   []int{0, 1, 1, 1, 0},
			expectedLastErrorIndex: make([]tempopb.PushErrorReason, numOfTraces),
		},
		{
			name:                   "all errors, first three traces",
			pushErrorByTrace:       []tempopb.PushErrorReason{traceTooLargeError, traceTooLargeError, traceTooLargeError},
			indexes:                []int{0, 1, 2},
			expectedSuccessIndex:   []int{0, 0, 0, 0, 0},
			expectedLastErrorIndex: []tempopb.PushErrorReason{traceTooLargeError, traceTooLargeError, traceTooLargeError, noError, noError},
		},
		{
			name:                   "random errors, random three traces",
			pushErrorByTrace:       []tempopb.PushErrorReason{traceTooLargeError, maxLiveTraceError, noError},
			indexes:                []int{0, 2, 4},
			expectedSuccessIndex:   []int{0, 0, 0, 0, 1},
			expectedLastErrorIndex: []tempopb.PushErrorReason{traceTooLargeError, noError, maxLiveTraceError, noError, noError},
		},
	}

	// prepare test data
	overridesConfig := overrides.Config{}
	buf := &bytes.Buffer{}
	logger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(buf))
	d, _ := prepare(t, overridesConfig, logger)

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			numSuccessByTraceIndex := make([]int, numOfTraces)
			lastErrorReasonByTraceIndex := make([]tempopb.PushErrorReason, numOfTraces)
			pushByteResponse := &tempopb.PushResponse{
				ErrorsByTrace: tc.pushErrorByTrace,
			}
			d.processPushResponse(pushByteResponse, numSuccessByTraceIndex, lastErrorReasonByTraceIndex, numOfTraces, tc.indexes)
			assert.Equal(t, numSuccessByTraceIndex, tc.expectedSuccessIndex)
			assert.Equal(t, lastErrorReasonByTraceIndex, tc.expectedLastErrorIndex)
		})
	}
}

func TestIngesterPushBytes(t *testing.T) {
	// prepare test data
	overridesConfig := overrides.Config{}
	buf := &bytes.Buffer{}
	logger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(buf))
	d, _ := prepare(t, overridesConfig, logger)

	traces := []*rebatchedTrace{
		{
			spanCount: 1,
		},
		{
			spanCount: 5,
		},
		{
			spanCount: 10,
		},
		{
			spanCount: 15,
		},
		{
			spanCount: 20,
		},
	}
	numOfTraces := len(traces)
	numSuccessByTraceIndex := make([]int, numOfTraces)
	lastErrorReasonByTraceIndex := make([]tempopb.PushErrorReason, numOfTraces)

	// 0 = trace_too_large, trace_too_large || discard count: 1
	// 1 = no error, trace_too_large || discard count: 5
	// 2 = no error, no error || discard count: 0
	// 3 = max_live, max_live || discard count: 15
	// 4 = trace_too_large, max_live || discard count: 20
	// total ttl: 6, mlt: 35

	batches := [][]int{
		{0, 1, 2},
		{1, 3},
		{0, 2},
		{3, 4},
		{4},
	}

	errorsByTraces := [][]tempopb.PushErrorReason{
		{traceTooLargeError, noError, noError},
		{traceTooLargeError, maxLiveTraceError},
		{traceTooLargeError, noError},
		{maxLiveTraceError, traceTooLargeError},
		{maxLiveTraceError},
	}

	for i, indexes := range batches {
		pushResponse := &tempopb.PushResponse{
			ErrorsByTrace: errorsByTraces[i],
		}
		d.processPushResponse(pushResponse, numSuccessByTraceIndex, lastErrorReasonByTraceIndex, numOfTraces, indexes)
	}

	maxLiveDiscardedCount, traceTooLargeDiscardedCount, _ := countDiscardedSpans(numSuccessByTraceIndex, lastErrorReasonByTraceIndex, traces, 3)
	assert.Equal(t, traceTooLargeDiscardedCount, 6)
	assert.Equal(t, maxLiveDiscardedCount, 35)
}

func TestPushTracesSkipMetricsGenerationIngestStorage(t *testing.T) {
	const topic = "test-topic"

	kafka, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.AllowAutoTopicCreation())
	require.NoError(t, err)
	t.Cleanup(kafka.Close)

	limitCfg := overrides.Config{}
	limitCfg.RegisterFlagsAndApplyDefaults(&flag.FlagSet{})

	distributorCfg, ingesterClientCfg, overridesSvc, _,
		ingesterRing, limits, middleware := setupDependencies(t, limitCfg)

	distributorCfg.KafkaWritePathEnabled = true
	distributorCfg.KafkaConfig = ingest.KafkaConfig{}
	distributorCfg.KafkaConfig.RegisterFlags(&flag.FlagSet{})
	distributorCfg.KafkaConfig.Address = kafka.ListenAddrs()[0]
	distributorCfg.KafkaConfig.Topic = topic

	d, err := New(
		distributorCfg,
		ingesterClientCfg,
		ingesterRing,
		generator_client.Config{},
		nil,
		singlePartitionRingReader{},
		overridesSvc,
		middleware,
		kitlog.NewLogfmtLogger(os.Stdout),
		limits,
		prometheus.NewRegistry(),
	)
	require.NoError(t, err)

	traces := batchesToTraces(t, []*v1.ResourceSpans{test.MakeBatch(10, nil)})

	reader, err := kgo.NewClient(kgo.SeedBrokers(kafka.ListenAddrs()...), kgo.ConsumeTopics(topic))
	require.NoError(t, err)

	t.Run("with no-generate-metrics header", func(t *testing.T) {
		// Inject the header into the incoming context. In a real call this would be done
		// by the gRPC server logic if the client sends that header in the outgoing
		// context.
		ctx := metadata.NewIncomingContext(ctx, metadata.Pairs(generator.NoGenerateMetricsContextKey, ""))
		_, err = d.PushTraces(ctx, traces)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var recordProcessed bool
		fetches := reader.PollFetches(ctx)
		fetches.EachRecord(func(record *kgo.Record) {
			recordProcessed = true
			req, err := ingest.NewDecoder().Decode(record.Value)
			require.NoError(t, err)
			require.True(t, req.SkipMetricsGeneration)

			reqs, err := ingest.NewPushBytesDecoder().Decode(record.Value)
			require.NoError(t, err)
			for req, err := range reqs {
				require.NoError(t, err)
				require.True(t, req.SkipMetricsGeneration)
			}
		})
		// Expect that we've fetched at least one record.
		require.True(t, recordProcessed)
	})

	t.Run("without no-generate-metrics header", func(t *testing.T) {
		_, err = d.PushTraces(ctx, traces)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var recordProcessed bool
		fetches := reader.PollFetches(ctx)
		fetches.EachRecord(func(record *kgo.Record) {
			recordProcessed = true
			req, err := ingest.NewDecoder().Decode(record.Value)
			require.NoError(t, err)
			require.False(t, req.SkipMetricsGeneration)

			reqs, err := ingest.NewPushBytesDecoder().Decode(record.Value)
			require.NoError(t, err)
			for req, err := range reqs {
				require.NoError(t, err)
				require.False(t, req.SkipMetricsGeneration)
			}
		})
		// Expect that we've fetched at least one record.
		require.True(t, recordProcessed)
	})
}

func TestArtificialLatency(t *testing.T) {
	// prepare test data
	overridesConfig := overrides.Config{}
	overridesConfig.RegisterFlagsAndApplyDefaults(&flag.FlagSet{})

	latency := 50 * time.Millisecond
	buf := &bytes.Buffer{}
	logger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(buf))
	d, _ := prepare(t, overridesConfig, logger)
	d.cfg.ArtificialDelay = latency

	batches := []*v1.ResourceSpans{
		makeResourceSpans("test-service", []*v1.ScopeSpans{
			makeScope(
				makeSpan("0a0102030405060708090a0b0c0d0e0f", "dad44adc9a83b370", "Test Span1", nil)),
			makeScope(
				makeSpan("bb42ec04df789ff04b10ea5274491685", "1b3a296034f4031e", "Test Span3", nil)),
		}),
	}

	traces := batchesToTraces(t, batches)
	reqStart := time.Now()
	_, err := d.PushTraces(ctx, traces)
	if err != nil {
		t.Fatal(err)
	}

	const tolerance = 10 * time.Millisecond
	assert.GreaterOrEqual(t, time.Since(reqStart)+tolerance, latency, "Expected artificial latency not respected")
}

func TestArtificialLatencyIsAppliedOnError(t *testing.T) {
	// prepare test data
	overridesConfig := overrides.Config{}
	overridesConfig.RegisterFlagsAndApplyDefaults(&flag.FlagSet{})

	latency := 50 * time.Millisecond
	buf := &bytes.Buffer{}
	logger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(buf))
	d, _ := prepare(t, overridesConfig, logger)
	d.cfg.ArtificialDelay = latency

	batches := []*v1.ResourceSpans{
		makeResourceSpans("test-service", []*v1.ScopeSpans{}),
	}

	traces := batchesToTraces(t, batches)
	reqStart := time.Now()
	_, err := d.PushTraces(ctx, traces)
	if err != nil {
		t.Fatal(err)
	}
	const tolerance = 10 * time.Millisecond
	assert.GreaterOrEqual(t, time.Since(reqStart)+tolerance, latency, "Expected artificial latency not respected")
}

type testLogSpan struct {
	Msg                string `json:"msg"`
	Level              string `json:"level"`
	PushErrorReason    string `json:"push_error_reason,omitempty"`
	Tenant             string `json:"tenant,omitempty"`
	TraceID            string `json:"traceid"`
	SpanID             string `json:"spanid"`
	Name               string `json:"span_name"`
	SpanStatus         string `json:"span_status,omitempty"`
	SpanKind           string `json:"span_kind,omitempty"`
	SpanServiceName    string `json:"span_service_name,omitempty"`
	SpanTag1           string `json:"span_tag1,omitempty"`
	SpanTag2           string `json:"span_tag2,omitempty"`
	ResourceAttribute1 string `json:"span_resource_attribute1,omitempty"`
	ResourceAttribute2 string `json:"span_resource_attribute2,omitempty"`
}

func makeAttribute(key, value string) *v1_common.KeyValue {
	return &v1_common.KeyValue{
		Key:   key,
		Value: &v1_common.AnyValue{Value: &v1_common.AnyValue_StringValue{StringValue: value}},
	}
}

func makeSpan(traceID, spanID, name string, status *v1.Status, attributes ...*v1_common.KeyValue) *v1.Span {
	if status == nil {
		status = &v1.Status{Code: v1.Status_STATUS_CODE_OK}
	}

	traceIDBytes, err := hex.DecodeString(traceID)
	if err != nil {
		panic(err)
	}
	spanIDBytes, err := hex.DecodeString(spanID)
	if err != nil {
		panic(err)
	}

	return &v1.Span{
		Name:       name,
		TraceId:    traceIDBytes,
		SpanId:     spanIDBytes,
		Status:     status,
		Kind:       v1.Span_SPAN_KIND_SERVER,
		Attributes: attributes,
	}
}

func makeScope(spans ...*v1.Span) *v1.ScopeSpans {
	return &v1.ScopeSpans{
		Scope: &v1_common.InstrumentationScope{
			Name:    "super library",
			Version: "0.0.1",
		},
		Spans: spans,
	}
}

func makeResourceSpans(serviceName string, ils []*v1.ScopeSpans, attributes ...*v1_common.KeyValue) *v1.ResourceSpans {
	rs := &v1.ResourceSpans{
		Resource: &v1_resource.Resource{
			Attributes: []*v1_common.KeyValue{
				{
					Key: "service.name",
					Value: &v1_common.AnyValue{
						Value: &v1_common.AnyValue_StringValue{
							StringValue: serviceName,
						},
					},
				},
			},
		},
		ScopeSpans: ils,
	}

	rs.Resource.Attributes = append(rs.Resource.Attributes, attributes...)

	return rs
}

func prepare(t *testing.T, limits overrides.Config, logger kitlog.Logger) (*Distributor, map[string]*mockIngester) {
	if logger == nil {
		logger = kitlog.NewNopLogger()
	}

	distributorConfig, clientConfig, overrides, ingesters, ingestersRing, l, mw := setupDependencies(t, limits)
	d, err := New(distributorConfig, clientConfig, ingestersRing, generator_client.Config{}, nil, nil, overrides, mw, logger, l, prometheus.NewPedanticRegistry())
	require.NoError(t, err)

	return d, ingesters
}

func setupDependencies(t *testing.T, limits overrides.Config) (Config, ingester_client.Config, overrides.Service, map[string]*mockIngester, *mockRing, dslog.Level, receiver.Middleware) {
	t.Helper()

	var (
		distributorConfig Config
		clientConfig      ingester_client.Config
	)
	flagext.DefaultValues(&clientConfig)

	overrides, err := overrides.NewOverrides(limits, nil, prometheus.DefaultRegisterer)
	require.NoError(t, err)

	// Mock the ingesters ring
	ingesters := map[string]*mockIngester{}
	for i := 0; i < numIngesters; i++ {
		ingesters[fmt.Sprintf("ingester%d", i)] = &mockIngester{
			pushBytes:   pushBytesNoOp,
			pushBytesV2: pushBytesNoOp,
		}
	}

	ingestersRing := &mockRing{
		replicationFactor: 3,
	}
	for addr := range ingesters {
		ingestersRing.ingesters = append(ingestersRing.ingesters, ring.InstanceDesc{
			Addr: addr,
		})
	}

	distributorConfig.MaxAttributeBytes = 1000
	distributorConfig.DistributorRing.HeartbeatPeriod = 100 * time.Millisecond
	distributorConfig.DistributorRing.InstanceID = strconv.Itoa(rand.Int())
	distributorConfig.DistributorRing.KVStore.Mock = nil
	distributorConfig.DistributorRing.InstanceInterfaceNames = []string{"eth0", "en0", "lo0"}
	distributorConfig.factory = func(addr string) (ring_client.PoolClient, error) {
		return ingesters[addr], nil
	}

	l := dslog.Level{}
	_ = l.Set("error")
	mw := receiver.MultiTenancyMiddleware()

	return distributorConfig, clientConfig, overrides, ingesters, ingestersRing, l, mw
}

type mockIngester struct {
	grpc_health_v1.HealthClient
	// pushBytes mock to be overridden in test scenarios if needed
	pushBytes func(ctx context.Context, in *tempopb.PushBytesRequest, opts ...grpc.CallOption) (*tempopb.PushResponse, error)
	// pushBytesV2 mock to be overridden in test scenarios if needed
	pushBytesV2 func(ctx context.Context, in *tempopb.PushBytesRequest, opts ...grpc.CallOption) (*tempopb.PushResponse, error)
}

func pushBytesNoOp(context.Context, *tempopb.PushBytesRequest, ...grpc.CallOption) (*tempopb.PushResponse, error) {
	return &tempopb.PushResponse{}, nil
}

var _ tempopb.PusherClient = (*mockIngester)(nil)

func (i *mockIngester) PushBytes(ctx context.Context, in *tempopb.PushBytesRequest, opts ...grpc.CallOption) (*tempopb.PushResponse, error) {
	return i.pushBytes(ctx, in, opts...)
}

func (i *mockIngester) PushBytesV2(ctx context.Context, in *tempopb.PushBytesRequest, opts ...grpc.CallOption) (*tempopb.PushResponse, error) {
	return i.pushBytesV2(ctx, in, opts...)
}

func (i *mockIngester) Close() error {
	return nil
}

// Copied from Cortex; TODO(twilkie) - factor this our and share it.
// mockRing doesn't do virtual nodes, just returns mod(key) + replicationFactor
// ingesters.
type mockRing struct {
	prometheus.Counter
	ingesters         []ring.InstanceDesc
	replicationFactor uint32
}

func (r mockRing) GetSubringForOperationStates(_ ring.Operation) ring.ReadRing {
	panic("implement me if required for testing")
}

func (r mockRing) WritableInstancesWithTokensCount() int {
	panic("implement me if required for testing")
}

func (r mockRing) WritableInstancesWithTokensInZoneCount(string) int {
	panic("implement me if required for testing")
}

var _ ring.ReadRing = (*mockRing)(nil)

func (r mockRing) Get(key uint32, _ ring.Operation, buf []ring.InstanceDesc, _, _ []string) (ring.ReplicationSet, error) {
	result := ring.ReplicationSet{
		MaxErrors: 1,
		Instances: buf[:0],
	}
	for i := uint32(0); i < r.replicationFactor; i++ {
		n := (key + i) % uint32(len(r.ingesters))
		result.Instances = append(result.Instances, r.ingesters[n])
	}
	return result, nil
}

func (r mockRing) GetWithOptions(key uint32, _ ring.Operation, _ ...ring.Option) (ring.ReplicationSet, error) {
	buf := make([]ring.InstanceDesc, 0)
	result := ring.ReplicationSet{
		MaxErrors: 1,
		Instances: buf,
	}
	for i := uint32(0); i < r.replicationFactor; i++ {
		n := (key + i) % uint32(len(r.ingesters))
		result.Instances = append(result.Instances, r.ingesters[n])
	}
	return result, nil
}

func (r mockRing) GetAllHealthy(ring.Operation) (ring.ReplicationSet, error) {
	return ring.ReplicationSet{
		Instances: r.ingesters,
		MaxErrors: 1,
	}, nil
}

func (r mockRing) GetReplicationSetForOperation(op ring.Operation) (ring.ReplicationSet, error) {
	return r.GetAllHealthy(op)
}

func (r mockRing) ReplicationFactor() int {
	return int(r.replicationFactor)
}

func (r mockRing) ShuffleShard(string, int) ring.ReadRing {
	return r
}

func (r mockRing) ShuffleShardWithLookback(string, int, time.Duration, time.Time) ring.ReadRing {
	return r
}

func (r mockRing) GetTokenRangesForInstance(_ string) (ring.TokenRanges, error) {
	return nil, nil
}

func (r mockRing) InstancesCount() int {
	return len(r.ingesters)
}

func (r mockRing) InstancesWithTokensCount() int {
	return len(r.ingesters)
}

func (r mockRing) HasInstance(string) bool {
	return true
}

func (r mockRing) CleanupShuffleShardCache(string) {
}

func (r mockRing) GetInstanceState(string) (ring.InstanceState, error) {
	return ring.ACTIVE, nil
}

func (r mockRing) InstancesInZoneCount(string) int {
	return 0
}

func (r mockRing) InstancesWithTokensInZoneCount(_ string) int {
	return 0
}

func (r mockRing) ZonesCount() int {
	return 0
}

type singlePartitionRingReader struct{}

func (m singlePartitionRingReader) PartitionRing() *ring.PartitionRing {
	desc := ring.PartitionRingDesc{
		Partitions: map[int32]ring.PartitionDesc{
			0: {Id: 0, Tokens: []uint32{0}, State: ring.PartitionActive},
		},
	}
	return ring.NewPartitionRing(desc)
}

func TestCheckForRateLimits(t *testing.T) {
	tests := []struct {
		name            string
		tracesSize      int
		rateLimitBytes  int
		burstLimitBytes int
		expectError     string
		errCode         codes.Code
	}{
		{
			name:            "size under rate limit and burst limit",
			tracesSize:      100,
			rateLimitBytes:  500,
			burstLimitBytes: 500,
			expectError:     "",
			errCode:         codes.OK,
		},
		{
			name:            "size exactly at rate limit and burst limit",
			tracesSize:      500,
			rateLimitBytes:  500,
			burstLimitBytes: 500,
			expectError:     "",
			errCode:         codes.OK,
		},
		{
			name:            "size over rate limit but exactly at burst rate limit",
			tracesSize:      500,
			rateLimitBytes:  200, // to test that burst is respected
			burstLimitBytes: 500,
			expectError:     "",
			errCode:         codes.OK,
		},
		{
			name:            "size over rate limit but under burst limit",
			tracesSize:      1100,
			rateLimitBytes:  500, // to test that burst is respected
			burstLimitBytes: 1500,
			expectError:     "",
			errCode:         codes.OK,
		},
		{
			name:            "size over rate limit and burst limit",
			tracesSize:      1100,
			rateLimitBytes:  500,
			burstLimitBytes: 500,
			expectError:     "RATE_LIMITED: batch size (1100 bytes) exceeds ingestion limit (local: 500 bytes/s, global: 0 bytes/s, burst: 500 bytes) while adding 1100 bytes for user test-user. consider reducing batch size or increasing rate limit.",
			errCode:         codes.ResourceExhausted,
		},
		{
			name:            "size over rate limit and burst limit",
			tracesSize:      1000,
			rateLimitBytes:  500,
			burstLimitBytes: 500,
			expectError:     "RATE_LIMITED: batch size (1000 bytes) exceeds ingestion limit (local: 500 bytes/s, global: 0 bytes/s, burst: 500 bytes) while adding 1000 bytes for user test-user. consider reducing batch size or increasing rate limit.",
			errCode:         codes.ResourceExhausted,
		},
		{
			name:            "size exactly at rate limit but over the burst limit",
			tracesSize:      500,
			rateLimitBytes:  500,
			burstLimitBytes: 200,
			expectError:     "RATE_LIMITED: ingestion rate limit (local: 500 bytes/s, global: 0 bytes/s, burst: 200 bytes) exceeded while adding 500 bytes for user test-user. consider increasing the limit or reducing ingestion rate.",
			errCode:         codes.ResourceExhausted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			overridesConfig := overrides.Config{
				Defaults: overrides.Overrides{
					Ingestion: overrides.IngestionOverrides{
						RateStrategy:   overrides.LocalIngestionRateStrategy,
						RateLimitBytes: tc.rateLimitBytes,
						BurstSizeBytes: tc.burstLimitBytes,
					},
				},
			}

			// Create a distributor with the overrides
			logger := kitlog.NewNopLogger()
			d, _ := prepare(t, overridesConfig, logger)

			// check if we can ingest the batch
			err := d.checkForRateLimits(tc.tracesSize, 100, "test-user")
			s, ok := status.FromError(err)
			require.True(t, ok)
			require.Equal(t, tc.errCode, s.Code())
			require.Equal(t, tc.expectError, s.Message())
		})
	}
}
