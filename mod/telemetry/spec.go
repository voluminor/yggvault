package telemetry

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/metric"
)

// // // // // // // // // //

// SpecObj describes one observable int64 instrument.
type SpecObj struct {
	Name    string
	Help    string
	Unit    string
	Counter bool
}

type registeredSpecObj struct {
	instrumentObj metric.Int64Observable
}

//

func counterOptionArr(specObj SpecObj) []metric.Int64ObservableCounterOption {
	optionArr := make([]metric.Int64ObservableCounterOption, 0, 2)
	if specObj.Help != "" {
		optionArr = append(optionArr, metric.WithDescription(specObj.Help))
	}
	if specObj.Unit != "" {
		optionArr = append(optionArr, metric.WithUnit(specObj.Unit))
	}
	return optionArr
}

func gaugeOptionArr(specObj SpecObj) []metric.Int64ObservableGaugeOption {
	optionArr := make([]metric.Int64ObservableGaugeOption, 0, 2)
	if specObj.Help != "" {
		optionArr = append(optionArr, metric.WithDescription(specObj.Help))
	}
	if specObj.Unit != "" {
		optionArr = append(optionArr, metric.WithUnit(specObj.Unit))
	}
	return optionArr
}

func registerInstrument(meterObj metric.Meter, specObj SpecObj) (metric.Int64Observable, error) {
	if specObj.Counter {
		return meterObj.Int64ObservableCounter(specObj.Name, counterOptionArr(specObj)...)
	}
	return meterObj.Int64ObservableGauge(specObj.Name, gaugeOptionArr(specObj)...)
}

//

// RegisterSpecs creates instruments from the table and registers the shared callback.
func RegisterSpecs(
	meterObj metric.Meter,
	specArr []SpecObj,
	observeFunc func() ([]int64, bool),
) (metric.Registration, error) {
	if meterObj == nil {
		return nil, nil
	}
	if observeFunc == nil {
		return nil, errors.New("telemetry observe function is nil")
	}
	registeredArr := make([]registeredSpecObj, 0, len(specArr))
	observableArr := make([]metric.Observable, 0, len(specArr))
	for _, specObj := range specArr {
		instrumentObj, err := registerInstrument(meterObj, specObj)
		if err != nil {
			return nil, err
		}
		registeredArr = append(registeredArr, registeredSpecObj{instrumentObj: instrumentObj})
		observableArr = append(observableArr, instrumentObj)
	}
	return meterObj.RegisterCallback(func(_ context.Context, observerObj metric.Observer) error {
		valueArr, ok := observeFunc()
		if !ok || len(valueArr) != len(registeredArr) {
			return nil
		}
		for i := range registeredArr {
			observerObj.ObserveInt64(registeredArr[i].instrumentObj, valueArr[i])
		}
		return nil
	}, observableArr...)
}
