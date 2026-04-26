package storage

import "errors"

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidTarget = errors.New("invalid target value")

	// ErrSystemTemplateImmutable is returned by TemplateStore.Delete when
	// the target row is a builtin (is_system = 1). Builtins may be
	// edited but never removed — see SPEC-SCHED2.3 for the operator
	// rationale.
	ErrSystemTemplateImmutable = errors.New("system template cannot be deleted")
)
