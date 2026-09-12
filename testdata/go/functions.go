package fixture

type Server struct{}

func top(value int) int {
	// comment-only line
	if value > 0 && value < 10 {
		inner := func(flag bool) int {
			if flag || value == 2 {
				return value
			}
			return 0
		}
		return inner(true)
	}
	switch value {
	case 20:
		return 20
	default:
		return -1
	}
}

func (Server) Serve(values []int) {
	for range values {
		select {
		case <-make(chan struct{}):
		default:
		}
	}
}

