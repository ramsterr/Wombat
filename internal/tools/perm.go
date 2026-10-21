package tools

// PermKind identifies why the user is being asked for permission. Each kind
// gets its own colour and wording in the interface so it is obvious whether
// the model is asking for ordinary tool access or for something outside the
// sandbox.
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
	// PermNetwork is network access the sandbox is blocking.
	PermNetwork PermKind = "network"
	// PermSandboxWrite is a write the sandbox is blocking.
	PermSandboxWrite PermKind = "sandbox-write"
	// PermDangerous is a command on the dangerous list.
	PermDangerous PermKind = "dangerous"
	// PermSensitive is a read of a credentials file.
	PermSensitive PermKind = "sensitive-read"
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
	case PermSandboxWrite:
		return "write access outside the sandbox"
	case PermDangerous:
		return "a dangerous command"
	case PermSensitive:
		return "a credentials file read"
	}
	return string(k)
}

// Outside reports whether the request goes beyond the sandbox, so the
// interface can mark it differently from ordinary tool permission.
func (k PermKind) Outside() bool {
	switch k {
	case PermNetwork, PermSandboxWrite, PermDangerous, PermOutside, PermSensitive:
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
	case PermDangerous:
		return PermDangerous
	case PermSensitive:
		return PermSensitive
	case PermNetwork:
		return PermNetwork
	case PermBash:
		return PermBash
	}
	return PermBash
}
