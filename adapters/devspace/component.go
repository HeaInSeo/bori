package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Component is the parsed .bori/component.yaml for one app.
type Component struct {
	Name        string `yaml:"name"`
	Port        int    `yaml:"port"`
	MetricsPath string `yaml:"metrics_path"`
	Namespace   string `yaml:"namespace"`
}

func loadComponent(path string) (Component, error) {
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
	return c, nil
}

// RegisteredApp pairs a Component with the .bori directory it was found in.
type RegisteredApp struct {
	BoriDir string
	Comp    Component
}

// discoverApps scans appsDir for subdirectories containing .bori/component.yaml.
func discoverApps(appsDir string) ([]RegisteredApp, error) {
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", appsDir, err)
	}
	var apps []RegisteredApp
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		boriDir := fmt.Sprintf("%s/%s/.bori", appsDir, e.Name())
		compPath := boriDir + "/component.yaml"
		if _, err := os.Stat(compPath); os.IsNotExist(err) {
			continue
		}
		comp, err := loadComponent(compPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		apps = append(apps, RegisteredApp{BoriDir: boriDir, Comp: comp})
	}
	return apps, nil
}
