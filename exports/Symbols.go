package exports

import "reflect"

// This shared map holds all the code Yaegi extracts
var Symbols = map[string]map[string]reflect.Value{}
