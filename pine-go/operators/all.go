// Package operators aggregates all built-in operator packages via blank imports.
// Importing this package registers every operator with the Pine engine.
//
//	import _ "github.com/Liam0205/pineapple/pine-go/operators"
package operators

import (
	_ "github.com/Liam0205/pineapple/pine-go/operators/bench"
	_ "github.com/Liam0205/pineapple/pine-go/operators/transform"
	_ "github.com/Liam0205/pineapple/pine-go/operators/filter"
	_ "github.com/Liam0205/pineapple/pine-go/operators/lua"
	_ "github.com/Liam0205/pineapple/pine-go/operators/merge"
	_ "github.com/Liam0205/pineapple/pine-go/operators/observe"
	_ "github.com/Liam0205/pineapple/pine-go/operators/recall"
	_ "github.com/Liam0205/pineapple/pine-go/operators/reorder"
)
