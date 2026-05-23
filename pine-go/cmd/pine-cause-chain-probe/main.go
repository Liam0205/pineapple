// pine-cause-chain-probe: smoke test for ExecutionError cause-chain unwrap.
//
// Constructs a FakeRedisError, wraps it in pine ExecutionError, then uses
// errors.As to recover the inner cause. Prints either:
//   PASS:<recovered_inner_msg>
// or
//   FAIL:<reason>
// on stdout. cross-validate Section 15 runs this probe against the same-
// shaped probe in pine-java / pine-python / pine-cpp and asserts stdout is
// byte-identical, confirming that the cause-chain capability stays at parity
// across runtimes.
package main

import (
	"errors"
	"fmt"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

type FakeRedisError struct {
	Key string
}

func (e *FakeRedisError) Error() string {
	return fmt.Sprintf("key=%s not found", e.Key)
}

func main() {
	inner := &FakeRedisError{Key: "user:42"}
	outer := &types.ExecutionError{Operator: "redis_getter", Err: inner}

	var recovered *FakeRedisError
	if errors.As(outer, &recovered) {
		fmt.Printf("PASS:%s\n", recovered.Error())
		return
	}
	fmt.Println("FAIL:errors.As did not recover FakeRedisError from ExecutionError chain")
}
