package util

import (
	"sort"
	"testing"
)

func TestReverseSecSlice_Less(t *testing.T) {
	type fields struct {
		StringSlice sort.StringSlice
	}
	type args struct {
		i int
		j int
	}
	slice := fields{sort.StringSlice([]string{
		"a.b.c",
		"a.b.c",
		"d.b.c",
		"d.a.b.c",
		"",
		"",
	})}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{{
		name:   "equal",
		fields: slice,
		args:   args{0, 1},
		want:   false,
	}, {
		name:   "less",
		fields: slice,
		args:   args{0, 2},
		want:   true,
	}, {
		name:   "length",
		fields: slice,
		args:   args{0, 3},
		want:   true,
	}, {
		name:   "single_empty",
		fields: slice,
		args:   args{0, 4},
		want:   false,
	}, {
		name:   "all_empty",
		fields: slice,
		args:   args{4, 5},
		want:   false,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &ReverseSecSlice{
				StringSlice: tt.fields.StringSlice,
			}
			if got := p.Less(tt.args.i, tt.args.j); got != tt.want {
				t.Errorf("ReverseSecSlice.Less() = %v, want %v", got, tt.want)
			}
		})
	}
}
