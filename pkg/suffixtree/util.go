package suffixtree

func GCSlice[T any](arr []T) []T {
	old := arr
	return append(make([]T, 0, len(old)), old...)
}
