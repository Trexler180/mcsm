//go:build !linux

package isolation

func resolve(cfg Config) *state {
	st := &state{cfg: cfg, status: Status{Mode: cfg.Mode}}
	switch cfg.Mode {
	case Off:
		st.status.Reason = "turned off (AGENT_ISOLATE_SERVERS=0): servers run as the agent's own user"
	default:
		st.status.Reason = "server isolation is only available on Linux"
	}
	return st
}

func wrap(_ *state, program string, args []string, _ string) (string, []string, error) {
	return program, args, nil
}
