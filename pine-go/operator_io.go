package pine

import "github.com/Liam0205/pineapple/pine-go/internal/types"

// Re-export OperatorInput and OperatorOutput from internal/types.

type OperatorInput = types.OperatorInput
type OperatorOutput = types.OperatorOutput

var NewOperatorInput = types.NewOperatorInput
var NewOperatorOutput = types.NewOperatorOutput
