package console

// must returns v, or panics when err is not nil: the assets are embedded in the binary, so a failure to read them
// is a build mistake that every start would hit.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
