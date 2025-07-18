package ingester

import (
	"flag"
	"os"
	"time"

	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/flagext"
	"github.com/grafana/dskit/ring"
	"github.com/grafana/tempo/pkg/ingest"

	"github.com/grafana/tempo/pkg/util/log"
	"github.com/grafana/tempo/tempodb"
	"github.com/grafana/tempo/tempodb/backend"
)

// Config for an ingester.
type Config struct {
	LifecyclerConfig      ring.LifecyclerConfig `yaml:"lifecycler,omitempty"`
	IngesterPartitionRing PartitionRingConfig   `yaml:"partition_ring" category:"experimental"`

	ConcurrentFlushes    int           `yaml:"concurrent_flushes"`
	FlushCheckPeriod     time.Duration `yaml:"flush_check_period"`
	FlushOpTimeout       time.Duration `yaml:"flush_op_timeout"`
	MaxTraceIdle         time.Duration `yaml:"trace_idle_period"`
	MaxTraceLive         time.Duration `yaml:"trace_live_period"`
	MaxBlockDuration     time.Duration `yaml:"max_block_duration"`
	MaxBlockBytes        uint64        `yaml:"max_block_bytes"`
	CompleteBlockTimeout time.Duration `yaml:"complete_block_timeout"`
	OverrideRingKey      string        `yaml:"override_ring_key"`
	FlushAllOnShutdown   bool          `yaml:"flush_all_on_shutdown"`
	FlushObjectStorage   bool          `yaml:"flush_object_storage"`

	// This config is dynamically injected because defined outside the ingester config.
	DedicatedColumns    backend.DedicatedColumns `yaml:"-"`
	IngestStorageConfig ingest.Config            `yaml:"-"`
}

// RegisterFlagsAndApplyDefaults registers the flags.
func (cfg *Config) RegisterFlagsAndApplyDefaults(prefix string, f *flag.FlagSet) {
	// apply generic defaults and then overlay tempo default
	flagext.DefaultValues(&cfg.LifecyclerConfig)
	cfg.LifecyclerConfig.RingConfig.KVStore.Store = "memberlist"
	cfg.LifecyclerConfig.RingConfig.ReplicationFactor = 1
	cfg.LifecyclerConfig.RingConfig.HeartbeatTimeout = 5 * time.Minute

	cfg.IngesterPartitionRing.RegisterFlags(f)

	cfg.ConcurrentFlushes = 4
	cfg.FlushCheckPeriod = 10 * time.Second
	cfg.FlushOpTimeout = 5 * time.Minute
	cfg.FlushAllOnShutdown = false
	cfg.FlushObjectStorage = true
	cfg.MaxTraceLive = 30 * time.Second

	f.DurationVar(&cfg.MaxTraceIdle, prefix+".trace-idle-period", 5*time.Second, "Duration after which to consider a trace complete if no spans have been received")
	f.DurationVar(&cfg.MaxBlockDuration, prefix+".max-block-duration", 30*time.Minute, "Maximum duration which the head block can be appended to before cutting it.")
	f.Uint64Var(&cfg.MaxBlockBytes, prefix+".max-block-bytes", 500*1024*1024, "Maximum size of the head block before cutting it.")
	f.DurationVar(&cfg.CompleteBlockTimeout, prefix+".complete-block-timeout", 3*tempodb.DefaultBlocklistPoll, "Duration to keep blocks in the ingester after they have been flushed.")
	f.StringVar(&cfg.LifecyclerConfig.Zone, prefix+".availability-zone", "", "Define Availability Zone in which this ingester is running.")

	hostname, err := os.Hostname()
	if err != nil {
		level.Error(log.Logger).Log("msg", "failed to get hostname", "err", err)
		os.Exit(1)
	}
	f.StringVar(&cfg.LifecyclerConfig.ID, prefix+".lifecycler.ID", hostname, "ID to register in the ring.")

	cfg.OverrideRingKey = ingesterRingKey
}
