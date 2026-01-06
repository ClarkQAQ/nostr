package nip86

import "encoding/json"

type Request struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func (r Request) MethodName() string { return r.Method }

type Response struct {
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}
