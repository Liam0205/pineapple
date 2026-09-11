package page.liam.pine;

import static org.junit.jupiter.api.Assertions.assertEquals;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Pins {@link GoFormat#marshalJson} to Go's json.Marshal for the composite
 * values an operator may turn into hash input (issue #201).
 *
 * <p>Every expected string was produced by running json.Marshal in Go on the
 * same value; none is derived by hand. The negative-zero case used
 * math.Copysign(0, -1) on the Go side because a Go constant -0.0 is just 0.
 */
class GoFormatMarshalJsonTest {

    private static List<Object> list(Object... xs) {
        List<Object> l = new ArrayList<>();
        for (Object x : xs) {
            l.add(x);
        }
        return l;
    }

    @Test
    void numbersUseGoSpellingNotJacksons() throws Exception {
        // The #201 shape: a Lua table {item_score*2, item_score*3} arrives as
        // List<Double>; a plain ObjectMapper writes "[28.0,42.0]".
        assertEquals("[28,42]", GoFormat.marshalJson(list(28.0, 42.0)));
        assertEquals("[2e+100,3e+100]", GoFormat.marshalJson(list(2e100, 3e100)));
        assertEquals("[1e-7,1e+21]", GoFormat.marshalJson(list(1e-7, 1e21)));
        assertEquals("[0.30000000000000004,-0]", GoFormat.marshalJson(list(0.30000000000000004, -0.0)));
        // Config literals decode to Integer; Go has only float64 here and
        // prints the same bytes for both.
        assertEquals("[28,42]", GoFormat.marshalJson(list(28, 42)));
    }

    @Test
    void mapKeysSortByUtf8BytesAtEveryDepth() throws Exception {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("b", 1.5);
        m.put("a", 0.1);
        assertEquals("{\"a\":0.1,\"b\":1.5}", GoFormat.marshalJson(m));

        Map<String, Object> inner = new LinkedHashMap<>();
        inner.put("y", 2.0);
        Map<String, Object> nested = new LinkedHashMap<>();
        nested.put("z", list(1.0, inner));
        assertEquals("{\"z\":[1,{\"y\":2}]}", GoFormat.marshalJson(nested));

        // UTF-8 byte order, not String.compareTo: U+10000 sorts after U+FFFD.
        Map<String, Object> nonAscii = new LinkedHashMap<>();
        nonAscii.put("é", 1.0);
        nonAscii.put("z", 2.0);
        nonAscii.put("𐀀", 3.0);
        nonAscii.put("�", 4.0);
        assertEquals("{\"z\":2,\"é\":1,\"�\":4,\"𐀀\":3}", GoFormat.marshalJson(nonAscii));
    }

    @Test
    void htmlCharactersEscapedLikeGo() throws Exception {
        assertEquals("[\"\\u003cx\\u003e\\u0026\"]", GoFormat.marshalJson(list("<x>&")));
    }

    @Test
    void emptyComposites() throws Exception {
        assertEquals("[]", GoFormat.marshalJson(list()));
        assertEquals("{}", GoFormat.marshalJson(new LinkedHashMap<String, Object>()));
    }
}
