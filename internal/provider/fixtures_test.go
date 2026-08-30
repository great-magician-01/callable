package provider

// pngHeader is a minimal PNG signature shared by the provider payload tests.
var pngHeader = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}

// weatherArgs is the shared tool-argument struct of the provider payload
// tests; the core agent tests keep their own copy.
type weatherArgs struct {
	City string `json:"city" jsonschema:"description=City name"`
	Unit string `json:"unit,omitempty" jsonschema:"description=Temperature unit,enum=celsius,enum=fahrenheit"`
}
