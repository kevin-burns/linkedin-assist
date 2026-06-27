package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// appConfig holds the persistent per-user configuration for li-assist.
// It is stored as JSON at ~/.config/li-assist/config.json (0600).
// Additional fields can be added here freely; the JSON decoder ignores
// unknown keys on read so old config files remain forward-compatible.
type appConfig struct {
	// HomeLocation is the default location used for job searches when
	// --location is not provided and --anywhere is not set.
	// Example: "Aachen, Germany"
	HomeLocation string `json:"home_location,omitempty"`

	// ConnectionsPath is the default path to the user's LinkedIn
	// Connections.csv export used by --intros. When empty, the default
	// path (~/.config/li-assist/connections.csv) is used.
	// Set via: li-assist config connections <path>
	// Override per-run via: --connections or LI_ASSIST_CONNECTIONS_CSV.
	ConnectionsPath string `json:"connections_path,omitempty"`
}

// defaultConfigPath returns the path to the JSON config file:
// ~/.config/li-assist/config.json.
// Returns an empty string when the home directory cannot be resolved
// (e.g. HOME unset in a container). Callers treat an empty path as
// "no config file available".
func defaultConfigPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "li-assist", "config.json")
}

// loadConfig reads the JSON config from path.
//   - Missing file → returns zero-value appConfig, nil error.
//   - Malformed JSON → prints WARNING to stderr, returns zero-value appConfig, nil error.
//   - Other OS error → returns zero-value appConfig and the error.
func loadConfig(path string) (appConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return appConfig{}, nil
		}
		return appConfig{}, err
	}

	var cfg appConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: config file %s is malformed (%v); treating as empty\n", path, err)
		return appConfig{}, nil
	}
	return cfg, nil
}

// saveConfig serialises cfg to JSON and writes it to path with mode 0600.
// It creates all parent directories as needed (matching the approach in exclude.go).
func saveConfig(path string, cfg appConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// Append a trailing newline for friendliness.
	raw = append(raw, '\n')

	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// newConfigCmd returns the "config" parent command.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage li-assist configuration",
		Long: `Manage persistent li-assist settings stored in ~/.config/li-assist/config.json.

Configuration values are used as defaults when the corresponding flag is
not explicitly provided on the command line.`,
	}
	cmd.AddCommand(newConfigLocationCmd())
	cmd.AddCommand(newConfigConnectionsCmd())
	return cmd
}

// newConfigLocationCmd returns the "config location" subcommand.
func newConfigLocationCmd() *cobra.Command {
	var clearFlag bool

	cmd := &cobra.Command{
		Use:   "location [value]",
		Short: "Get or set the default home location for job searches",
		Long: `Get or set the home_location in ~/.config/li-assist/config.json.

The home location is used as the default value for --location on
'jobs search' and 'jobs sweep' when neither --location nor --anywhere is given.

Examples:

  # Set home location
  li-assist config location "Aachen, Germany"

  # Print current home location
  li-assist config location

  # Clear home location
  li-assist config location --clear`,
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath := defaultConfigPath()
			if cfgPath == "" {
				return fmt.Errorf("cannot determine config path: home directory is not set")
			}

			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			switch {
			case clearFlag:
				// Clear the home location.
				if len(args) > 0 {
					return fmt.Errorf("--clear and a value cannot be used together")
				}
				cfg.HomeLocation = ""
				if err := saveConfig(cfgPath, cfg); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
				fmt.Fprintln(os.Stdout, "home_location cleared")

			case len(args) == 1:
				// Set a new value.
				cfg.HomeLocation = args[0]
				if err := saveConfig(cfgPath, cfg); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
				fmt.Fprintf(os.Stdout, "home_location set to %q\n", cfg.HomeLocation)

			default:
				// Print current value.
				if cfg.HomeLocation == "" {
					fmt.Fprintln(os.Stdout, "home_location is not set")
				} else {
					fmt.Fprintf(os.Stdout, "home_location: %q\n", cfg.HomeLocation)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&clearFlag, "clear", false, "Clear the home_location setting")
	return cmd
}

// newConfigConnectionsCmd returns the "config connections" subcommand.
func newConfigConnectionsCmd() *cobra.Command {
	var clearFlag bool

	cmd := &cobra.Command{
		Use:   "connections [path]",
		Short: "Get or set the default path to your LinkedIn Connections.csv export",
		Long: `Get or set the connections_path in ~/.config/li-assist/config.json.

The connections path is used as the default location for --connections on
'jobs get' and 'jobs sweep' when the --connections flag is not given and the
LI_ASSIST_CONNECTIONS_CSV environment variable is not set.

Path resolution precedence for --intros:
  1. --connections <path>            (flag, highest priority)
  2. LI_ASSIST_CONNECTIONS_CSV       (environment variable)
  3. connections_path in config.json  (this setting)
  4. ~/.config/li-assist/connections.csv (default)

To export your connections: LinkedIn → Settings → Data Privacy →
Get a copy of your data → Connections.

Examples:

  # Set connections path
  li-assist config connections ~/Downloads/Connections.csv

  # Print current connections path
  li-assist config connections

  # Clear connections path (revert to default)
  li-assist config connections --clear`,
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath := defaultConfigPath()
			if cfgPath == "" {
				return fmt.Errorf("cannot determine config path: home directory is not set")
			}

			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			switch {
			case clearFlag:
				if len(args) > 0 {
					return fmt.Errorf("--clear and a value cannot be used together")
				}
				cfg.ConnectionsPath = ""
				if err := saveConfig(cfgPath, cfg); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
				fmt.Fprintln(os.Stdout, "connections_path cleared")

			case len(args) == 1:
				cfg.ConnectionsPath = args[0]
				if err := saveConfig(cfgPath, cfg); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
				fmt.Fprintf(os.Stdout, "connections_path set to %q\n", cfg.ConnectionsPath)

			default:
				if cfg.ConnectionsPath == "" {
					fmt.Fprintln(os.Stdout, "connections_path is not set")
				} else {
					fmt.Fprintf(os.Stdout, "connections_path: %q\n", cfg.ConnectionsPath)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&clearFlag, "clear", false, "Clear the connections_path setting")
	return cmd
}
