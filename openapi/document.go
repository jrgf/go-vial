package openapi

// Fixed OpenAPI objects are typed; JSON Schema values remain dynamic because
// callers can supply either an object or a boolean schema.
type document struct {
	OpenAPI           string                                   `json:"openapi"`
	JSONSchemaDialect string                                   `json:"jsonSchemaDialect"`
	Info              documentInfo                             `json:"info"`
	Paths             map[string]map[string]*operationDocument `json:"paths"`
	Components        *componentsDocument                      `json:"components,omitempty"`
	Security          *[]SecurityRequirement                   `json:"security,omitempty"`
}

type documentInfo struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type componentsDocument struct {
	SecuritySchemes map[string]securityDocument `json:"securitySchemes"`
}

type operationDocument struct {
	OperationID  string                      `json:"operationId,omitempty"`
	Summary      string                      `json:"summary,omitempty"`
	Description  string                      `json:"description,omitempty"`
	Tags         []string                    `json:"tags,omitempty"`
	Parameters   []parameterDocument         `json:"parameters,omitempty"`
	RequestBody  *requestBodyDocument        `json:"requestBody,omitempty"`
	Responses    map[string]responseDocument `json:"responses"`
	Security     *[]SecurityRequirement      `json:"security,omitempty"`
	Pattern      string                      `json:"x-vial-pattern,omitempty"`
	Alternatives []*operationDocument        `json:"x-vial-alternatives,omitempty"`
}

type parameterDocument struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required,omitempty"`
	Schema   any    `json:"schema"`
}

type requestBodyDocument struct {
	Required bool                     `json:"required,omitempty"`
	Content  map[string]mediaDocument `json:"content"`
}

type mediaDocument struct {
	Schema any `json:"schema"`
}

type responseDocument struct {
	Description string                   `json:"description"`
	Content     map[string]mediaDocument `json:"content,omitempty"`
}

type securityDocument struct {
	Type         string `json:"type"`
	Description  string `json:"description,omitempty"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
	Name         string `json:"name,omitempty"`
	In           string `json:"in,omitempty"`
}
