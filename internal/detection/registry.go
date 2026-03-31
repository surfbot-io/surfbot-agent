package detection

// Registry holds all available detection tools in pipeline order.
type Registry struct {
	tools []DetectionTool
}

// NewRegistry creates a registry with all detection tools in pipeline phase order.
func NewRegistry() *Registry {
	return &Registry{
		tools: []DetectionTool{
			NewSubfinderTool(),
			NewDNSXTool(),
			NewNaabuTool(),
			NewHTTPXTool(),
			NewNucleiTool(),
		},
	}
}

// NewRegistryFrom creates a registry from the given tools (useful for testing).
func NewRegistryFrom(tools []DetectionTool) *Registry {
	return &Registry{tools: tools}
}

// Tools returns all registered tools.
func (r *Registry) Tools() []DetectionTool { return r.tools }

// GetByName returns the tool with the given name, or nil and false.
func (r *Registry) GetByName(name string) (DetectionTool, bool) {
	for _, t := range r.tools {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

// AvailableTools returns only tools where Available() is true.
func (r *Registry) AvailableTools() []DetectionTool {
	var available []DetectionTool
	for _, t := range r.tools {
		if t.Available() {
			available = append(available, t)
		}
	}
	return available
}
