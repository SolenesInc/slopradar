package typescript

import (
	"fmt"
	"path"
	"strings"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/oxc"
)

func Analyze(file string, source []byte) (lang.Result, error) {
	if !supported(file) {
		return lang.Result{}, fmt.Errorf("unsupported TypeScript or JavaScript file %q", file)
	}
	return oxc.Analyze(file, source)
}

func supported(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case ".ts", ".mts", ".cts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}
