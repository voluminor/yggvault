package telemetry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

// // // // // // // // // //

const cPushContentType = "text/plain; version=0.0.4"

const cPushResponseLimit = 4096

// //

func (obj *Obj) pushLoop(ctx context.Context) {
	tickerObj := time.NewTicker(obj.pushEvery)
	defer tickerObj.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-obj.doneCh:
			return
		case <-tickerObj.C:
			obj.pushOnce(ctx)
		}
	}
}

func (obj *Obj) pushOnce(ctx context.Context) {
	snapObj := obj.snap.Load()
	if snapObj == nil || len(snapObj.doc) == 0 {
		return
	}
	bodyArr := snapObj.doc

	obj.pushTotal.Add(ctx, 1)
	obj.pushBytes.Add(ctx, int64(len(bodyArr)))

	requestObj, err := http.NewRequestWithContext(ctx, http.MethodPost, obj.pushURL, bytes.NewReader(bodyArr))
	if err != nil {
		obj.pushFailures.Add(ctx, 1)
		return
	}
	requestObj.Header.Set("Content-Type", cPushContentType)

	responseObj, err := obj.pushClient.Do(requestObj)
	if err != nil {
		obj.pushFailures.Add(ctx, 1)
		return
	}
	defer responseObj.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(responseObj.Body, cPushResponseLimit))
	if responseObj.StatusCode < http.StatusOK || responseObj.StatusCode >= http.StatusMultipleChoices {
		obj.pushFailures.Add(ctx, 1)
	}
}
