package webui

import (
	"sync"

	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/view"
)

// // // // // // // // // //

var (
	rendererOnce sync.Once
	rendererObj  *view.RendererObj
	rendererErr  error
)

// // // // // // // // // //

func renderer() (*view.RendererObj, error) {
	rendererOnce.Do(func() { rendererObj, rendererErr = view.New() })
	return rendererObj, rendererErr
}

func classification(keyStateObj state.KeyStateObj) string {
	if keyStateObj.Classified {
		return keyStateObj.Classification.String()
	}
	return ""
}

func originOf(keyStateObj state.KeyStateObj) view.SourceObj {
	return view.SourceObj{
		URL:            keyStateObj.SourceURL,
		Status:         keyStateObj.Availability.String(),
		Classification: classification(keyStateObj),
	}
}
