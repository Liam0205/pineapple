// Package operators aggregates all built-in operator packages via blank imports.
// Importing this package registers every operator with the Pine engine.
//
//	import _ "github.com/Liam0205/pineapple/operators"
package operators

import (
	_ "github.com/Liam0205/pineapple/operators/feature"
	_ "github.com/Liam0205/pineapple/operators/filter"
	_ "github.com/Liam0205/pineapple/operators/lua"
	_ "github.com/Liam0205/pineapple/operators/merge"
	_ "github.com/Liam0205/pineapple/operators/observe"
	_ "github.com/Liam0205/pineapple/operators/recall"
	_ "github.com/Liam0205/pineapple/operators/reorder"
)
