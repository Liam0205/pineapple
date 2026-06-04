package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.GoTypeNames;
import page.liam.pine.OperatorParams;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;
import page.liam.pine.PineErrors;

import java.util.Map;

/**
 * Operator: filter_truncate
 * Metadata contract
 *   CommonInput:  []
 *   CommonOutput: []
 *   ItemInput:    []
 *   ItemOutput:   []
 */
public class FilterTruncate extends AbstractOperator implements page.liam.pine.ConsumesRowSet, page.liam.pine.MutatesRowSet {
    private long topN;

    @Override
    public void init(OperatorParams params) {
        Object v = params.get("top_n");
        if (v instanceof Number) {
            topN = ((Number) v).longValue();
        } else if (v instanceof String) {
            // Templatable marker (e.g. "{{user_tier_limit}}"). The
            // per-request value arrives via input.templatedParam at
            // execute time; the engine guarantees this fallback is
            // never read.
            topN = 0;
        } else {
            throw new IllegalArgumentException("filter_truncate: top_n must be numeric, got " + GoTypeNames.of(v));
        }
        if (topN < 0) {
            throw new IllegalArgumentException("filter_truncate: top_n must be non-negative, got " + topN);
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        // top_n is templatable (#74). When the DSL configured a {{field}}
        // marker the engine resolved it against this request's common
        // frame before execute; otherwise the init-time value is used.
        // The Number cast is unreachable: top_n is declared int64 and
        // TemplateResolver normalizes via parseInt -> Long.
        long effective = topN;
        Object resolved = input.templatedParam("top_n");
        if (resolved instanceof Number) {
            effective = ((Number) resolved).longValue();
        }
        // Mirror Init's invariant at execute time: a templated negative
        // value would otherwise silently remove every item (Math.min
        // returns the negative, loop start removes from i < 0), masking
        // a configuration bug.
        if (effective < 0) {
            throw new PineErrors.OperatorException("filter_truncate: top_n must be non-negative, got " + effective);
        }
        for (int i = (int) Math.min(effective, input.itemCount()); i < input.itemCount(); i++) {
            output.removeItem(i);
        }
    }
}
