package main

import "github.com/HeaInSeo/bori/pkg/component"

// Type aliases so the rest of this package compiles without changes.
type Component = component.Component
type RegisteredApp = component.RegisteredApp

func discoverApps(appsDir string) ([]RegisteredApp, error) { return component.Discover(appsDir) }
