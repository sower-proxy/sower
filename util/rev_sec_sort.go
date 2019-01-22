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
