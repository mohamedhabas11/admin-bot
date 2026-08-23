package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mohammedhabas11/admin-bot/internal/app"
	"github.com/mohammedhabas11/admin-bot/pkg/config"
)

var (
	validatePath = flag.String("validate", "", "Path to config file to validate only.")
	configPath   = flag.String("config", "", "Path to config file (overrides ENV var).")
)

// buildVersion is stamped at link time via -ldflags "-X main.buildVersion=...".
var buildVersion = "dev"

const configPathEnvVar = "ADMINBOT_CONFIG_PATH"

func main() {
	flag.Parse()

	if *validatePath != "" {
		fmt.Printf("Validating configuration file: %s\n", *validatePath)
		if err := config.ValidateConfigFile(*validatePath); err != nil {
			fmt.Fprintf(os.Stderr, "Validation Failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Configuration file is valid.")
		os.Exit(0)
	}

	finalConfigPath := resolveConfigPath()
	slog.Info("starting admin-bot")

	reloadChan := make(chan bool, 1)
	initialCfg, err := config.LoadConfig(finalConfigPath, reloadChan)
	if err != nil {
		slog.Error("failed to load initial configuration", "path", finalConfigPath, "err", err)
		os.Exit(1)
	}

	application := app.New(buildVersion)
	application.Start(initialCfg)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	slog.Info("application started; press Ctrl+C to shut down")

	for running := true; running; {
		select {
		case sig := <-signalChan:
			slog.Info("shutdown signal received; starting graceful shutdown", "signal", sig)
			running = false
			application.Shutdown()

		case <-reloadChan:
			slog.Info("reload signal received; checking for necessary restarts")
			application.Reload(config.GetConfig())
		}
	}

	slog.Info("waiting for background tasks to complete")
	application.Wait()
	slog.Info("application exiting")
}

// resolveConfigPath applies the precedence: -config flag, then environment,
// then the default "./config.yaml".
func resolveConfigPath() string {
	if *configPath != "" {
		slog.Info("using config path from -config flag", "path", *configPath)
		return *configPath
	}
	if envPath := os.Getenv(configPathEnvVar); envPath != "" {
		slog.Info("using config path from environment", "env", configPathEnvVar, "path", envPath)
		return envPath
	}
	slog.Info("using default config path", "path", "config.yaml")
	return "config.yaml"
}
