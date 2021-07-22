package deferlog

import (
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func InfoWarn(err error) *zerolog.Event {
	if err != nil {
		return Logger.Warn().Err(err)
	}

	return Logger.Info()
}
func InfoFatal(err error) *zerolog.Event {
	if err != nil {
		return Logger.Fatal().Err(err)
	}

	return Logger.Info()
}

func DebugWarn(err error) *zerolog.Event {
	if err != nil {
		return Logger.Warn().Err(err)
	}

	return Logger.Debug()
}
func DebugError(err error) *zerolog.Event {
	if err != nil {
		return Logger.Error().Err(err)
	}

	return Logger.Debug()
}
func DebugFatal(err error) *zerolog.Event {
	if err != nil {
		return Logger.Fatal().Err(err)
	}

	return Logger.Debug()
}

type StdLogger struct {
	*zerolog.Logger
}

var Std = StdLogger{
	Logger: &log.Logger,
}

func (std *StdLogger) InfoWarn(err error) *zerolog.Event {
	if err != nil {
		return Std.Logger.Warn().Err(err)
	}

	return Std.Logger.Info()
}
func (std *StdLogger) InfoFatal(err error) *zerolog.Event {
	if err != nil {
		return Std.Logger.Fatal().Err(err)
	}

	return Std.Logger.Info()
}

func (std *StdLogger) DebugWarn(err error) *zerolog.Event {
	if err != nil {
		return Std.Logger.Warn().Err(err)
	}

	return Std.Logger.Debug()
}
func (std *StdLogger) DebugError(err error) *zerolog.Event {
	if err != nil {
		return Std.Logger.Error().Err(err)
	}

	return Std.Logger.Debug()
}
func (std *StdLogger) DebugFatal(err error) *zerolog.Event {
	if err != nil {
		return Std.Logger.Fatal().Err(err)
	}

	return Std.Logger.Debug()
}
