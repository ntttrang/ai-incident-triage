package httpadapter

// ErrorResponse is a standard API error body.
type ErrorResponse struct {
	Error string `json:"error" example:"invalid input"`
}

// HealthResponse is returned by health endpoints.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}
