package pipeline

import (
	"bytes"
	"context"
	"testing"

	"github.com/go-kit/log"
	"github.com/gogo/protobuf/jsonpb"
	"github.com/grafana/tempo/pkg/api"
	"github.com/grafana/tempo/pkg/cache"
	"github.com/grafana/tempo/pkg/tempopb"
	"github.com/grafana/tempo/pkg/util/test"
	"github.com/stretchr/testify/require"
)

func TestNilProvider(t *testing.T) {
	c := newFrontendCache(nil, cache.RoleFrontendSearch, log.NewNopLogger())
	require.Nil(t, c)
}

func TestCacheCaches(t *testing.T) {
	expected := &tempopb.SearchTagsResponse{
		TagNames: []string{"foo", "bar"},
	}

	// marshal mesage to bytes
	buf := bytes.NewBuffer([]byte{})
	err := (&jsonpb.Marshaler{}).Marshal(buf, expected)
	require.NoError(t, err)

	testKey := "key"
	testData := buf.Bytes()

	p := test.NewMockProvider()
	c := newFrontendCache(p, cache.RoleBloom, log.NewNopLogger())
	require.NotNil(t, c)

	// create response
	c.store(context.Background(), testKey, testData)

	actual := &tempopb.SearchTagsResponse{}
	buffer := c.fetchBytes(context.Background(), testKey)
	err = (&jsonpb.Unmarshaler{AllowUnknownFields: true}).Unmarshal(bytes.NewReader(buffer), actual)

	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestDetermineContentType(t *testing.T) {
	// Create and marshal a real protobuf message
	protoMsg := &tempopb.SearchTagsResponse{
		TagNames: []string{"foo", "bar"},
	}
	protobufContent, err := protoMsg.Marshal()
	require.NoError(t, err)

	// Also create JSON content for comparison
	jsonBuf := bytes.NewBuffer([]byte{})
	err = (&jsonpb.Marshaler{}).Marshal(jsonBuf, protoMsg)
	require.NoError(t, err)
	jsonContent := jsonBuf.Bytes()

	testCases := []struct {
		name         string
		body         []byte
		expectedType string
	}{
		{
			name:         "JSON content",
			body:         jsonContent,
			expectedType: api.HeaderAcceptJSON,
		},
		{
			name:         "Protobuf content",
			body:         protobufContent,
			expectedType: api.HeaderAcceptProtobuf,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			contentType := determineContentType(tc.body)
			require.Equal(t, tc.expectedType, contentType)
		})
	}
}
