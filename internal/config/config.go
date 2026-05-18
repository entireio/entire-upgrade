package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Greeting string `json:"greeting"`
}

func Default() Config {
	return Config{Greeting: "Hello from an Entire plugin"}
}

func Path(dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("plugin data dir is empty")
	}
	return filepath.Join(dataDir, "config.json"), nil
}

func Load(dataDir string) (Config, error) {
	path, err := Path(dataDir)
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Greeting == "" {
		cfg.Greeting = Default().Greeting
	}
	return cfg, nil
}

func Save(dataDir string, cfg Config) error {
	path, err := Path(dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := replaceFile(tmpName, path); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func replaceFile(oldPath, newPath string) error {
	if err := os.Rename(oldPath, newPath); err == nil {
		return nil
	}

	if err := os.Remove(newPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(oldPath, newPath)
}
