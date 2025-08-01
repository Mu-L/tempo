package usagestats

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/grafana/dskit/kv"
	jsoniter "github.com/json-iterator/go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/grafana/tempo/tempodb/backend"
	"github.com/grafana/tempo/tempodb/backend/local"
)

func Test_LeaderElection(t *testing.T) {
	stabilityCheckInterval = 100 * time.Millisecond

	result := make(chan *ClusterSeed, 10)

	objectClient, err := local.NewBackend(&local.Config{
		Path: t.TempDir(),
	})
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		go func() {
			r, leaderErr := NewReporter(Config{Leader: true, Enabled: true}, kv.Config{
				Store: "inmemory",
			}, objectClient, objectClient, log.NewNopLogger(), nil)
			require.NoError(t, leaderErr)
			r.init(context.Background())
			result <- r.cluster
		}()
	}
	for i := 0; i < 7; i++ {
		go func() {
			r, nonLeaderError := NewReporter(Config{Leader: false, Enabled: true}, kv.Config{
				Store: "inmemory",
			}, objectClient, objectClient, log.NewNopLogger(), nil)
			require.NoError(t, nonLeaderError)
			r.init(context.Background())
			result <- r.cluster
		}()
	}

	var UID []string
	for i := 0; i < 10; i++ {
		cluster := <-result
		require.NotNil(t, cluster)
		UID = append(UID, cluster.UID)
	}
	first := UID[0]
	for _, uid := range UID {
		require.Equal(t, first, uid)
	}
	kvClient, err := kv.NewClient(kv.Config{Store: "inmemory"}, JSONCodec, nil, log.NewNopLogger())
	require.NoError(t, err)
	// verify that the ID found is also correctly stored in the kv store and not overridden by another leader.
	data, err := kvClient.Get(context.Background(), seedKey)
	require.NoError(t, err)
	t.Logf("data: %+v", data.(*ClusterSeed))
	require.Equal(t, data.(*ClusterSeed).UID, first)
}

func Test_LeaderElectionWithBrokenSeedFile(t *testing.T) {
	stabilityCheckInterval = 100 * time.Millisecond

	result := make(chan *ClusterSeed, 10)

	objectClient, err := local.NewBackend(&local.Config{
		Path: t.TempDir(),
	})
	require.NoError(t, err)

	// Ensure that leader election succeeds even when the seed file has been
	// corrupted.  This means that we don't need to extend the interface of the
	// backend in order to delete a corrupted seed file.
	err = objectClient.Write(context.Background(), backend.ClusterSeedFileName, []string{}, bytes.NewReader([]byte("{")), 1, nil)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		go func() {
			r, leaderErr := NewReporter(Config{Leader: true, Enabled: true}, kv.Config{
				Store: "inmemory",
			}, objectClient, objectClient, log.NewNopLogger(), nil)
			require.NoError(t, leaderErr)
			r.init(context.Background())
			result <- r.cluster
		}()
	}
	for i := 0; i < 7; i++ {
		go func() {
			r, nonLeaderError := NewReporter(Config{Leader: false, Enabled: true}, kv.Config{
				Store: "inmemory",
			}, objectClient, objectClient, log.NewNopLogger(), nil)
			require.NoError(t, nonLeaderError)
			r.init(context.Background())
			result <- r.cluster
		}()
	}

	var UID []string
	for i := 0; i < 10; i++ {
		cluster := <-result
		require.NotNil(t, cluster)
		UID = append(UID, cluster.UID)
	}
	first := UID[0]
	for _, uid := range UID {
		require.Equal(t, first, uid)
	}
	kvClient, err := kv.NewClient(kv.Config{Store: "inmemory"}, JSONCodec, nil, log.NewNopLogger())
	require.NoError(t, err)
	// verify that the ID found is also correctly stored in the kv store and not overridden by another leader.
	data, err := kvClient.Get(context.Background(), seedKey)
	require.NoError(t, err)
	t.Logf("data: %+v", data.(*ClusterSeed))
	require.Equal(t, data.(*ClusterSeed).UID, first)
}

func Test_ReportLoop(t *testing.T) {
	// stub
	reportCheckInterval = 100 * time.Millisecond
	reportInterval = time.Second
	stabilityCheckInterval = 100 * time.Millisecond

	totalReport := 0
	clusterIDs := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var received Report
		totalReport++
		require.NoError(t, jsoniter.NewDecoder(r.Body).Decode(&received))
		clusterIDs = append(clusterIDs, received.ClusterID)
		rw.WriteHeader(http.StatusOK)
	}))
	usageStatsURL = server.URL

	objectClient, err := local.NewBackend(&local.Config{
		Path: t.TempDir(),
	})

	require.NoError(t, err)

	r, err := NewReporter(Config{Leader: true, Enabled: true}, kv.Config{
		Store: "inmemory",
	}, objectClient, objectClient, log.NewNopLogger(), prometheus.NewPedanticRegistry())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	r.initLeader(ctx)

	go func() {
		<-time.After(6*time.Second + (stabilityCheckInterval * time.Duration(stabilityMinimumRequired+1)))
		cancel()
	}()
	require.Equal(t, nil, r.running(ctx))
	require.GreaterOrEqual(t, totalReport, 5)
	first := clusterIDs[0]
	for _, uid := range clusterIDs {
		require.Equal(t, first, uid)
	}
	require.Equal(t, first, r.cluster.UID)
}

func Test_NextReport(t *testing.T) {
	fixtures := map[string]struct {
		interval  time.Duration
		createdAt time.Time
		now       time.Time

		next time.Time
	}{
		"createdAt aligned with interval and now": {
			interval:  1 * time.Hour,
			createdAt: time.Unix(0, time.Hour.Nanoseconds()),
			now:       time.Unix(0, 2*time.Hour.Nanoseconds()),
			next:      time.Unix(0, 2*time.Hour.Nanoseconds()),
		},
		"createdAt aligned with interval": {
			interval:  1 * time.Hour,
			createdAt: time.Unix(0, time.Hour.Nanoseconds()),
			now:       time.Unix(0, 2*time.Hour.Nanoseconds()+1),
			next:      time.Unix(0, 3*time.Hour.Nanoseconds()),
		},
		"createdAt not aligned": {
			interval:  1 * time.Hour,
			createdAt: time.Unix(0, time.Hour.Nanoseconds()+18*time.Minute.Nanoseconds()+20*time.Millisecond.Nanoseconds()),
			now:       time.Unix(0, 2*time.Hour.Nanoseconds()+1),
			next:      time.Unix(0, 2*time.Hour.Nanoseconds()+18*time.Minute.Nanoseconds()+20*time.Millisecond.Nanoseconds()),
		},
	}
	for name, f := range fixtures {
		t.Run(name, func(t *testing.T) {
			next := nextReport(f.interval, f.createdAt, f.now)
			require.Equal(t, f.next, next)
		})
	}
}

func TestWrongKV(t *testing.T) {
	objectClient, err := local.NewBackend(&local.Config{
		Path: t.TempDir(),
	})
	require.NoError(t, err)

	r, err := NewReporter(Config{Leader: true, Enabled: true}, kv.Config{
		Store: "",
	}, objectClient, objectClient, log.NewNopLogger(), prometheus.NewPedanticRegistry())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-time.After(1 * time.Second)
		cancel()
	}()
	require.Equal(t, context.Canceled, r.running(ctx))
}
