package kind

type Kind string

const (
	State      Kind = "State"
	Choice     Kind = "Choice"
	Initial    Kind = "Initial"
	Final      Kind = "Final"
	External   Kind = "External"
	Local      Kind = "Local"
	Internal   Kind = "Internal"
	Self       Kind = "Self"
	Transition Kind = "Transition"
)
