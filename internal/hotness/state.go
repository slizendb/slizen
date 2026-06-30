package hotness

type State string

const (
	StateCold    State = "COLD"
	StateWarm    State = "WARM"
	StateHot     State = "HOT"
	StateCooling State = "COOLING"
)

type Transition struct {
	Key  string
	From State
	To   State
}
