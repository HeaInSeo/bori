package component

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Component is the parsed .bori/component.yaml for one app.
type Component struct {
	Name        string `yaml:"name"`
	Image       string `yaml:"image"`
	Port        int    `yaml:"port"`
	MetricsPath string `yaml:"metrics_path"`
	Namespace   string `yaml:"namespace"`
}

// Load parses a component.yaml file and applies defaults.
func Load(path string) (Component, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Component{}, fmt.Errorf("read: %w", err)
	}
	var c Component
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Component{}, fmt.Errorf("parse: %w", err)
	}
	if c.MetricsPath == "" {
		c.MetricsPath = "/metrics"
	}
	if c.Port == 0 {
		c.Port = 8080
	}
	if c.Namespace == "" {
		c.Namespace = c.Name
	}
	return c, nil
}

// RegisteredApp pairs a Component with the .bori directory it was found in.
type RegisteredApp struct {
	BoriDir string
	Comp    Component
}

// Discover scans appsDir for subdirectories containing .bori/component.yaml.
func Discover(appsDir string) ([]RegisteredApp, error) {
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", appsDir, err)
	}
	var apps []RegisteredApp
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		boriDir := appsDir + "/" + e.Name() + "/.bori"
		compPath := boriDir + "/component.yaml"
		if _, err := os.Stat(compPath); os.IsNotExist(err) {
			continue
		}
		comp, err := Load(compPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		apps = append(apps, RegisteredApp{BoriDir: boriDir, Comp: comp})
	}
	return apps, nil
}
