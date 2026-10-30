package authtest

// must returns v, or panics when err is not nil: the helpers here make test fixtures, which cannot go on without them.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// must2 is must for a call that returns two values.
func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}
