package daemon

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/kardianos/service"
)

// ServiceName is the canonical kardianos/service identifier. The same name
// is used for the systemd unit, the launchd label, and the Windows service
// registration so all CLI subcommands target the same OS-level service.
const ServiceName = "surfbot"

// Config bundles everything Build needs to construct a kardianos service
// wired up to the surfbot daemon Runner.
type Config struct {
	Mode           Mode
	Paths          Paths
	Version        string
	ShutdownGrace  time.Duration
	Heartbeat      time.Duration
	ConfigOverride string // optional --config path baked into the unit
}

// Service is the kardianos/service.Interface implementation. Start spawns
// the runner non-blockingly; Stop cancels the runner context and waits up
// to ShutdownGrace.
type Service struct {
	cfg    Config
	runner *Runner
	logger *Logger
}

// Build wires up Logger + Runner + kardianos service.Service. The returned
// service.Service is what callers invoke Install/Uninstall/Start/Stop/Run
// against.
func Build(cfg Config) (*Service, service.Service, error) {
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = 20 * time.Second
	}
	if cfg.Heartbeat == 0 {
		cfg.Heartbeat = 30 * time.Second
	}
	if err := cfg.Paths.EnsureDirs(cfg.Mode); err != nil {
		return nil, nil, fmt.Errorf("creating daemon dirs: %w", err)
	}

	logger := NewLogger(cfg.Paths.LogFile(), LoggerOptions{Compress: true})
	state := NewStateStore(cfg.Paths.StateFile())
	runner := NewRunner(RunnerConfig{
		Scheduler: NewNoopScheduler(),
		State:     state,
		Logger:    logger,
		Heartbeat: cfg.Heartbeat,
		Version:   cfg.Version,
	})

	s := &Service{cfg: cfg, runner: runner, logger: logger}
	svcCfg := buildServiceConfig(cfg)
	svc, err := service.New(s, svcCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("constructing service: %w", err)
	}
	return s, svc, nil
}

// buildServiceConfig translates surfbot daemon Config into the kardianos
// service.Config the OS-specific backends consume. Mode controls the
// "UserService" key which selects LaunchAgent vs LaunchDaemon on macOS
// and per-user systemd units on Linux.
func buildServiceConfig(cfg Config) *service.Config {
	args := []string{"daemon"}
	if cfg.Mode == ModeUser {
		args = append(args, "--user")
	} else {
		args = append(args, "--system")
	}
	if cfg.ConfigOverride != "" {
		args = append(args, "--config", cfg.ConfigOverride)
	}
	args = append(args, "run")

	return &service.Config{
		Name:        ServiceName,
		DisplayName: "Surfbot Agent",
		Description: "Continuous local attack-surface monitoring",
		Arguments:   args,
		Option: service.KeyValue{
			"UserService": cfg.Mode == ModeUser,
			"RunAtLoad":   true,
			"KeepAlive":   true,
			"LogOutput":   true,
		},
	}
}

// Start is invoked by the OS service manager. It must return promptly;
// the actual work runs in a goroutine owned by Runner.
func (s *Service) Start(_ service.Service) error {
	s.logger.Slog().Info("daemon starting",
		"version", s.cfg.Version,
		"mode", s.cfg.Mode.String(),
		"goos", runtime.GOOS,
	)
	return s.runner.Start()
}

// Stop is invoked by the OS service manager. It cancels the runner and
// waits up to ShutdownGrace; on timeout the OS will SIGKILL us, which is
// the documented contract.
func (s *Service) Stop(_ service.Service) error {
	s.logger.Slog().Info("daemon stopping", "grace", s.cfg.ShutdownGrace)
	if err := s.runner.Stop(s.cfg.ShutdownGrace); err != nil {
		if errors.Is(err, ErrShutdownTimeout) {
			s.logger.Slog().Warn("daemon forced stop: in-flight scan aborted")
		} else {
			s.logger.Slog().Error("runner stop error", "err", err)
		}
	}
	if cerr := s.logger.Close(); cerr != nil {
		// Best effort: nothing left to log to.
		fmt.Fprintln(os.Stderr, "closing daemon logger:", cerr)
	}
	return nil
}

// Logger exposes the slog logger so the cli layer can use the same writer
// for non-runner messages (e.g. install confirmations).
func (s *Service) Logger() *Logger { return s.logger }
