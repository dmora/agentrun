package agentrun

// CloneModelCatalog returns a deep copy of models.
func CloneModelCatalog(models []ModelInfo) []ModelInfo {
	if models == nil {
		return nil
	}
	cloned := make([]ModelInfo, len(models))
	copy(cloned, models)
	for i := range cloned {
		cloned[i].Aliases = append([]string(nil), models[i].Aliases...)
	}
	return cloned
}

// ModelAvailable reports whether id matches a canonical ID or alias in a
// finite catalog. It returns false for an empty catalog.
func ModelAvailable(models []ModelInfo, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
		for _, alias := range model.Aliases {
			if alias == id {
				return true
			}
		}
	}
	return false
}

// ValidateModelSelection validates id when models is a finite, non-empty
// catalog. An empty catalog means discovery was unavailable or inconclusive,
// so selection is preserved for the backend to handle.
func ValidateModelSelection(models []ModelInfo, id string) error {
	if id == "" || len(models) == 0 || ModelAvailable(models, id) {
		return nil
	}
	return &ModelNotSupportedError{Model: id, Available: CloneModelCatalog(models)}
}
