package page.liam.pine.debug;

/**
 * Identifies the current operator invocation's position in a plan: the
 * operator name, its configured {@code type_name}, and the name of the
 * {@code pipeline_map} entry that directly contains it ({@code ""} for a
 * top-level node). Session or request identity is supplied by the
 * application's {@link Emitter}. Mirrors pine-go {@code pkg/debug.NodeMeta}.
 */
public record NodeMeta(String operator, String type, String subFlow) {
    /** The metadata of an event emitted outside any operator. */
    public static final NodeMeta NONE = new NodeMeta("", "", "");
}
