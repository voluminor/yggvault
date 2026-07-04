package telemetry

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/voluminor/yggvault/target"
	stcfg "github.com/voluminor/yggvault/target/stconf"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// // // // // // // // // //

// New builds telemetry from validated config.
// When no exposure method is enabled, it returns a disabled noop provider without SDK allocation or loops.
func New(configObj *stcfg.ConfigObj) (*Obj, error) {
	if configObj == nil {
		return nil, errors.New("telemetry config is nil")
	}

	metricsObj := configObj.Metrics
	exposed := metricsObj.Web.Public || metricsObj.Ygg.Public ||
		metricsObj.Web.Internal || metricsObj.Ygg.Internal || metricsObj.Push.Enabled
	if !exposed {
		return &Obj{
			enabled:  false,
			provider: noop.NewMeterProvider(),
			doneCh:   make(chan struct{}),
		}, nil
	}

	readerObj := sdkmetric.NewManualReader()
	resourceObj := resource.NewSchemaless(attribute.String("service.name", target.Name))
	providerObj := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(readerObj),
		sdkmetric.WithResource(resourceObj),
	)

	obj := &Obj{
		enabled:       true,
		provider:      providerObj,
		sdkProvider:   providerObj,
		reader:        readerObj,
		snapshotEvery: positiveOr(metricsObj.SnapshotInterval, time.Second),
		doneCh:        make(chan struct{}),
	}

	if metricsObj.Push.Enabled {
		obj.pushEnabled = true
		obj.pushURL = strings.TrimRight(strings.TrimSpace(metricsObj.Push.Url), "/") + cPushPath
		obj.pushEvery = positiveOr(metricsObj.Push.Interval, 15*time.Second)
		obj.pushClient = &http.Client{Timeout: metricsObj.Push.Timeout}
	}

	if err := obj.registerInternal(); err != nil {
		return nil, err
	}

	return obj, nil
}

func positiveOr(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}
