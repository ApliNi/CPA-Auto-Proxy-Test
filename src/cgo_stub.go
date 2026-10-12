//go:build !cgo

package main

import (
	"encoding/json"
	"errors"
)

func (hostCallbacks) call(method string, payload any) (json.RawMessage, error) {
	return nil, errors.New("cgo is disabled in current build")
}
