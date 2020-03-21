package util

import (
	"sort"
	"strings"
)

type ReverseSecSlice struct {
	sort.StringSlice
}

func NewReverseSecSlice(a []string) *ReverseSecSlice {
	return &ReverseSecSlice{sort.StringSlice(a)}
}

func (p *ReverseSecSlice) Sort() *ReverseSecSlice {
	sort.Sort(p)
	return p
}
func (p *ReverseSecSlice) Uniq() []string {
	olds := []string(p.StringSlice)

	last := ""
	strs := make([]string, 0, len(olds))
	for _, str := range olds {
		if str != last {
			strs = append(strs, str)
		}
		last = str
	}
	return strs
}

func (p *ReverseSecSlice) Less(i, j int) bool {
	secsI := strings.Split(p.StringSlice[i], ".")
	secsJ := strings.Split(p.StringSlice[j], ".")

	lenI := len(secsI) - 1
	lenJ := len(secsJ) - 1
	length := lenI
	if lenI > lenJ {
		length = lenJ
	}

	for idx := 0; idx <= length; idx++ {
		if secsI[lenI-idx] == secsJ[lenJ-idx] {
			continue
		}
		return secsI[lenI-idx] < secsJ[lenJ-idx]
	}
	return lenI < lenJ
}
