package tools

// PermKind identifies why the user is being asked for permission. Each kind
// gets its own colour and wording in the interface so it is obvious whether
// the model is asking for ordinary tool access or for something outside the
// workspace.
type PermKind string

const (
	// PermBash is a shell command.
	PermBash PermKind = "bash"
	// PermWrite is a file write.
	PermWrite PermKind = "write"
	// PermEdit is a file edit.
	PermEdit PermKind = "edit"
	// PermOutside is a write or edit outside the working directory.
	PermOutside PermKind = "outside"
	// PermNetwork is network access.
	PermNetwork PermKind = "network"
	// PermDangerous is a command on the dangerous list.
	PermDangerous PermKind = "dangerous"
)

// Label is the short name shown in a prompt.
func (k PermKind) Label() string {
	switch k {
	case PermBash:
		return "bash"
	case PermWrite:
		return "write"
	case PermEdit:
		return "edit"
	case PermOutside:
		return "write outside the workspace"
	case PermNetwork:
		return "network access"
	case PermDangerous:
		return "a dangerous command"
	}
	return string(k)
}

// Outside reports whether the request goes beyond the workspace, so the
// interface can mark it differently from ordinary tool permission.
func (k PermKind) Outside() bool {
	switch k {
	case PermNetwork, PermDangerous, PermOutside:
		return true
	}
	return false
}

// PermKindFor maps a tool permission class to its kind.
func PermKindFor(class string) PermKind {
	switch PermKind(class) {
	case PermWrite:
		return PermWrite
	case PermEdit:
		return PermEdit
	case PermOutside:
		return PermOutside
	}
	return PermBash
}
