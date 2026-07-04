package main

import (
	"errors"
	"math"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"strconv"
)

// // // // // // // // // //

func clampSeconds(nextFunc http.HandlerFunc, maxSeconds uint64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queryObj := r.URL.Query()
		if rawText := queryObj.Get("seconds"); rawText != "" {
			if secondsValue, err := strconv.ParseFloat(rawText, 64); err == nil &&
				(math.IsNaN(secondsValue) || math.IsInf(secondsValue, 0) || secondsValue > float64(maxSeconds)) {
				queryObj.Set("seconds", strconv.FormatUint(maxSeconds, 10))
				r.URL.RawQuery = queryObj.Encode()
			}
		}
		nextFunc(w, r)
	}
}

func newProfilingMux(maxSeconds uint64) *http.ServeMux {
	muxObj := http.NewServeMux()
	muxObj.HandleFunc("/debug/pprof/", pprof.Index)
	muxObj.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	muxObj.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	muxObj.HandleFunc("/debug/pprof/profile", clampSeconds(pprof.Profile, maxSeconds))
	muxObj.HandleFunc("/debug/pprof/trace", clampSeconds(pprof.Trace, maxSeconds))
	return muxObj
}

// // // // // // // // // //

func (rt *runtimeObj) startProfiling() error {
	profilingCfg := rt.configObj.Profiling
	if !profilingCfg.Enabled {
		return nil
	}
	if profilingCfg.Contention.BlockRate > 0 {
		runtime.SetBlockProfileRate(int(profilingCfg.Contention.BlockRate))
	}
	if profilingCfg.Contention.MutexFraction > 0 {
		runtime.SetMutexProfileFraction(int(profilingCfg.Contention.MutexFraction))
	}
	netListener, err := net.Listen("tcp", profilingCfg.Listen)
	if err != nil {
		return err
	}
	rt.profiling = &http.Server{
		Handler:           newProfilingMux(uint64(profilingCfg.MaxProfileSeconds)),
		ReadHeaderTimeout: rt.configObj.Web.Ingress.ReadHeaderTimeout,
		IdleTimeout:       rt.configObj.Web.Ingress.IdleTimeout,
	}
	go func() {
		if serveErr := rt.profiling.Serve(netListener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			rt.loggerObj.Zero().Error().Err(serveErr).Msg("pprof listener stopped")
		}
	}()
	return nil
}
