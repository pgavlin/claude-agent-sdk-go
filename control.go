package agentsdk

// ControlRequest represents a control protocol request sent over stdin/stdout.
type ControlRequest struct {
	Type      string         `json:"type"` // "control_request"
	RequestID string         `json:"request_id"`
	Request   map[string]any `json:"request"`
}

// ControlResponse represents a control protocol response.
type ControlResponse struct {
	Type     string                 `json:"type"` // "control_response"
	Response ControlResponsePayload `json:"response"`
}

// ControlResponsePayload contains the response data.
type ControlResponsePayload struct {
	Subtype   string         `json:"subtype"` // "success" or "error"
	RequestID string         `json:"request_id"`
	Response  map[string]any `json:"response,omitempty"`
	Error     string         `json:"error,omitempty"`
}
