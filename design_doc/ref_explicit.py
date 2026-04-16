flow = SomeFlowClass(name = "example")

flow.op_a(
  common_input = ["common_foo", "common_bar"],
  common_output = ["common_baz"],
  item_input = ["item_foo"],
  item_output = ["item_baz"],
  other_params = "some_value"
) \
.op_b(
  common_input = ["common_baz"],
  item_input = ["item_baz"],
  item_output = ["item_qux"],
  other_params = "some_value"
)
