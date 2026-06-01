package main

import (
	"context"

	"github.com/HeaInSeo/bori/pkg/collect"
)

// scrapeMetrics delegates to pkg/collect.ScrapeMetrics.
func scrapeMetrics(ctx context.Context, comp Component) (map[string]float64, error) {
	return collect.ScrapeMetrics(ctx, collect.Target{
		Namespace:   comp.Namespace,
		ServiceName: comp.Name,
		Port:        comp.Port,
		MetricsPath: comp.MetricsPath,
	})
}
