package app

import "flag"

func addFlag[T any](flagSet *flag.FlagSet, value *T, short, long string, defaultValue T, usage string) {
	switch v := any(value).(type) {
	case *string:
		if short != "" {
			flagSet.StringVar(v, short, any(defaultValue).(string), usage)
		}
		if long != "" {
			flagSet.StringVar(v, long, any(defaultValue).(string), usage)
		}
	case *int:
		if short != "" {
			flagSet.IntVar(v, short, any(defaultValue).(int), usage)
		}
		if long != "" {
			flagSet.IntVar(v, long, any(defaultValue).(int), usage)
		}
	case *bool:
		if short != "" {
			flagSet.BoolVar(v, short, any(defaultValue).(bool), usage)
		}
		if long != "" {
			flagSet.BoolVar(v, long, any(defaultValue).(bool), usage)
		}
	default:
		panic("unsupported flag type")
	}
}
