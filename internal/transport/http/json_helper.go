package httpx

import "encoding/json"

// jsonMarshalImpl is the actual implementation; split out so handlers can
// use `jsonMarshal` without importing encoding/json repeatedly.
func jsonMarshalImpl(v any) ([]byte, error) {
	return json.Marshal(v)
}
