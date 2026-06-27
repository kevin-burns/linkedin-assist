// Package voyager contains the Voyager API client and response parsers for
// linkedin-assist. Parsers are pure functions on []byte and carry no dependency
// on a browser, chromedp, or HTTP server; replay tests feed corpus bytes directly.
package voyager

import "encoding/json"

// voyagerEnvelope is the outer wrapper returned by all Voyager REST endpoints:
//
//	{ "data": {...}, "included": [ {entityUrn, $type, ...}, ... ] }
//
// included is a polymorphic flat array. We decode it as raw JSON messages so
// individual parsers can pick only the fields they care about.
type voyagerEnvelope struct {
	Data     json.RawMessage   `json:"data"`
	Included []json.RawMessage `json:"included"`
}

// entityStub is decoded from every included element to extract its URN.
type entityStub struct {
	EntityUrn string `json:"entityUrn"`
}

// buildIncludedMap indexes the included array by entityUrn.
// Each value is the raw JSON for that entity, allowing callers to decode only
// the typed fields they need.
func buildIncludedMap(included []json.RawMessage) map[string]json.RawMessage {
	m := make(map[string]json.RawMessage, len(included))
	for _, raw := range included {
		var stub entityStub
		if err := json.Unmarshal(raw, &stub); err != nil {
			// Skip entries we cannot unmarshal -- the array is polymorphic and
			// some entries may have unexpected shapes; we only need the URN key.
			continue
		}
		if stub.EntityUrn != "" {
			m[stub.EntityUrn] = raw
		}
	}
	return m
}
