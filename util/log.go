package util

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
)

var StructLogger = zerolog.New(os.Stdout).
	With().Caller().Timestamp().Logger()

var ConsoleLogger = zerolog.New(zerolog.ConsoleWriter{
	Out:        os.Stdout,
	TimeFormat: time.StampMilli,
	FormatCaller: func(i interface{}) string {
		caller := i.(string)
		if idx := strings.Index(caller, "/pkg/mod/"); idx > 0 {
			return caller[idx+9:]
		}
		if idx := strings.LastIndexByte(caller, '/'); idx > 0 {
			return caller[idx+1:]
		}
		return caller
	},
}).With().Timestamp().Caller().Logger()

func init() {
	zerolog.ErrorStackMarshaler = func(err error) interface{} {
		return pkgerrors.MarshalStack(err)
	}
	if fi, _ := os.Stdout.Stat(); (fi.Mode() & os.ModeCharDevice) == 0 {
		log.Logger = StructLogger
	} else {
		log.Logger = ConsoleLogger
	}
}
