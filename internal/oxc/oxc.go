package oxc

/*
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/lib/darwin_arm64 -lslopradar_oxc -lm
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/lib/darwin_amd64 -lslopradar_oxc -lm
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/lib/linux_arm64 -lslopradar_oxc -ldl -lpthread -lm
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/lib/linux_amd64 -lslopradar_oxc -ldl -lpthread -lm
#include "oxc.h"
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"path"
	"runtime"
	"strings"
	"unsafe"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

type analysis struct {
	Status      string           `json:"status"`
	Diagnostics []diagnostic     `json:"diagnostics"`
	Functions   []functionMetric `json:"functions"`
	Comments    []lang.Span      `json:"comments"`
	Tokens      []token          `json:"tokens"`
}

type diagnostic struct {
	Message string `json:"message"`
}

type functionMetric struct {
	Name   string           `json:"name"`
	Line   int              `json:"line"`
	CC     int              `json:"cc"`
	SLOC   int              `json:"sloc"`
	Nested []functionMetric `json:"nested"`
}

type token struct {
	Text string `json:"text"`
	Line int    `json:"line"`
}

func Analyze(file string, source []byte) (lang.Result, error) {
	extension := path.Ext(file)
	parserFile := strings.TrimSuffix(file, extension) + strings.ToLower(extension)
	fileBytes := []byte(parserFile)
	var output C.slopradar_oxc_buffer
	status := C.slopradar_oxc_analyze(
		bytePointer(fileBytes), C.size_t(len(fileBytes)),
		bytePointer(source), C.size_t(len(source)),
		&output,
	)
	runtime.KeepAlive(fileBytes)
	runtime.KeepAlive(source)
	defer C.slopradar_oxc_release(&output)
	payload := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(output.data)), int(output.len))...)
	if status != 0 {
		return lang.Result{}, fmt.Errorf("native Oxc bridge status=%d: %s", int(status), payload)
	}

	var decoded analysis
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return lang.Result{}, fmt.Errorf("decode native Oxc result: %w", err)
	}
	if decoded.Status != "ok" {
		messages := make([]string, 0, len(decoded.Diagnostics))
		for _, item := range decoded.Diagnostics {
			messages = append(messages, item.Message)
		}
		return lang.Result{}, fmt.Errorf("parse %s: invalid TypeScript or JavaScript syntax: %s", file, strings.Join(messages, "; "))
	}

	result := lang.Result{
		Functions:       make([]model.Function, 0, len(decoded.Functions)),
		FunctionBuckets: make([]model.Bucket, 0, len(decoded.Functions)),
		Comments:        decoded.Comments,
		Tokens:          make([]lang.Token, 0, len(decoded.Tokens)),
		TestSpans:       []lang.Span{},
		Warnings:        []string{},
	}
	for _, item := range decoded.Functions {
		result.Functions = append(result.Functions, convertFunction(file, item))
		result.FunctionBuckets = append(result.FunctionBuckets, model.Source)
	}
	for _, item := range decoded.Tokens {
		result.Tokens = append(result.Tokens, lang.Token{Text: item.Text, Line: item.Line, Bucket: model.Source})
	}
	return result, nil
}

func bytePointer(data []byte) *C.uint8_t {
	if len(data) == 0 {
		return nil
	}
	return (*C.uint8_t)(unsafe.Pointer(&data[0]))
}

func convertFunction(file string, item functionMetric) model.Function {
	var nested []model.Function
	for _, child := range item.Nested {
		nested = append(nested, convertFunction(file, child))
	}
	return model.NewFunction(file, item.Name, item.Line, item.CC, item.SLOC, nested)
}
