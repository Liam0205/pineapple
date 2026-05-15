package page.liam.pine.operators;

import page.liam.pine.*;

import java.nio.charset.StandardCharsets;
import java.util.*;

public class ReorderShuffle extends AbstractOperator {

    @Override
    public void init(Map<String, Object> params) {}

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        int n = input.itemCount();
        if (n == 0) return;

        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < commonInput.size(); i++) {
            if (i > 0) sb.append('|');
            sb.append(anyToString(input.common(commonInput.get(i))));
        }
        sb.append('|');
        String saltPrefix = sb.toString();

        String itemField = itemInput.get(0);
        double[] ranks = new double[n];
        long[] ids = new long[n];
        Integer[] indices = new Integer[n];

        for (int i = 0; i < n; i++) {
            String itemVal = anyToString(input.item(i, itemField));
            String key = saltPrefix + itemVal;
            ranks[i] = hashToUnitInterval(key);
            ids[i] = parseUint64(itemVal);
            indices[i] = i;
        }

        Arrays.sort(indices, (a, b) -> {
            int cmp = Double.compare(ranks[a], ranks[b]);
            if (cmp != 0) return cmp;
            return Long.compare(ids[a], ids[b]);
        });

        List<Integer> order = new ArrayList<>(n);
        for (int idx : indices) {
            order.add(idx);
        }
        output.setItemOrder(order);
    }

    private static double hashToUnitInterval(String s) {
        long h = fnv64a(s);
        double unsigned = (h & 0x7FFFFFFFFFFFFFFFL) + (h < 0 ? 9223372036854775808.0 : 0.0);
        return unsigned / 18446744073709551616.0;
    }

    private static long fnv64a(String s) {
        byte[] data = s.getBytes(StandardCharsets.UTF_8);
        long hash = 0xcbf29ce484222325L;
        for (byte b : data) {
            hash ^= (b & 0xFF);
            hash *= 0x100000001b3L;
        }
        return hash;
    }

    private static String anyToString(Object v) {
        if (v == null) return "";
        if (v instanceof String) return (String) v;
        if (v instanceof Number) {
            double d = ((Number) v).doubleValue();
            if (d == (long) d && !Double.isInfinite(d)) return Long.toString((long) d);
            return formatFloatG(d);
        }
        return v.toString();
    }

    private static String formatFloatG(double d) {
        String s = Double.toString(d);
        // Normalize Java's scientific notation (1.5E8) to Go's format (1.5e+08)
        int eIdx = s.indexOf('E');
        if (eIdx < 0) return s;
        String mantissa = s.substring(0, eIdx);
        String expStr = s.substring(eIdx + 1);
        int exp = Integer.parseInt(expStr);
        if (exp >= 0) {
            return mantissa + "e+" + String.format("%02d", exp);
        } else {
            return mantissa + "e-" + String.format("%02d", -exp);
        }
    }

    private static long parseUint64(String s) {
        try {
            return Long.parseUnsignedLong(s);
        } catch (NumberFormatException e) {
            return 0L;
        }
    }
}
