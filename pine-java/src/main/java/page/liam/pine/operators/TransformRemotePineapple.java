package page.liam.pine.operators;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import page.liam.pine.*;

import java.io.*;
import java.net.*;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.*;

/**
 * Operator: transform_by_remote_pineapple
 * Metadata contract
 *   CommonInput:  [<local_common_fields...>]
 *   CommonOutput: [<local_common_output_fields...>]
 *   ItemInput:    [<local_item_fields...>]
 *   ItemOutput:   [<local_item_output_fields...>]
 */
public class TransformRemotePineapple extends AbstractOperator implements ConcurrentSafe {
    private static final ObjectMapper mapper = new ObjectMapper();
    private static final long DEFAULT_MAX_RESPONSE = 10L * 1024 * 1024;

    private String url;
    private String host;
    private Duration timeout;
    private boolean failOnError = true;
    private boolean allowPrivate;
    private long maxResponseSize = DEFAULT_MAX_RESPONSE;
    private HttpClient client;

    private List<String> commonReq = Collections.emptyList();
    private List<String> itemReq = Collections.emptyList();
    private List<String> commonResp = Collections.emptyList();
    private List<String> itemResp = Collections.emptyList();

    @Override
    public void init(Map<String, Object> params) {
        host = (String) params.getOrDefault("host", "");
        long port = toLong(params.getOrDefault("port", 0));
        String endpoint = (String) params.getOrDefault("endpoint", "/execute");
        if (endpoint.isEmpty()) endpoint = "/execute";

        url = "http://" + host + ":" + port + endpoint;

        double timeoutSec = 5.0;
        Object t = params.get("timeout");
        if (t instanceof Number) timeoutSec = ((Number) t).doubleValue();
        timeout = Duration.ofMillis((long) (timeoutSec * 1000));

        Object foe = params.get("fail_on_error");
        if (foe instanceof Boolean) failOnError = (Boolean) foe;

        Object mrs = params.get("max_response_size");
        if (mrs instanceof Number) maxResponseSize = ((Number) mrs).longValue();

        Object ap = params.get("allow_private");
        if (ap instanceof Boolean) allowPrivate = (Boolean) ap;

        commonReq = toStringList(params.get("common_request"));
        itemReq = toStringList(params.get("item_request"));
        commonResp = toStringList(params.get("common_response"));
        itemResp = toStringList(params.get("item_response"));

        if (!allowPrivate) {
            validateHost(host);
        }

        client = HttpClient.newBuilder()
                .connectTimeout(timeout)
                .build();
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        List<String> cReq = commonReq.isEmpty() ? commonInput : commonReq;
        List<String> iReq = itemReq.isEmpty() ? itemInput : itemReq;
        List<String> cResp = commonResp.isEmpty() ? commonOutput : commonResp;
        List<String> iResp = itemResp.isEmpty() ? itemOutput : itemResp;

        Map<String, Object> reqCommon = new LinkedHashMap<>();
        for (int i = 0; i < commonInput.size() && i < cReq.size(); i++) {
            reqCommon.put(cReq.get(i), input.common(commonInput.get(i)));
        }

        List<Map<String, Object>> reqItems = new ArrayList<>(input.itemCount());
        for (int j = 0; j < input.itemCount(); j++) {
            Map<String, Object> item = new LinkedHashMap<>();
            for (int i = 0; i < itemInput.size() && i < iReq.size(); i++) {
                item.put(iReq.get(i), input.item(j, itemInput.get(i)));
            }
            reqItems.add(item);
        }

        Map<String, Object> reqBody = new LinkedHashMap<>();
        reqBody.put("common", reqCommon);
        reqBody.put("items", reqItems);

        byte[] body;
        try {
            body = mapper.writeValueAsBytes(reqBody);
        } catch (Exception e) {
            throw new PineErrors.OperatorException("transform_by_remote_pineapple: serialize request: " + e.getMessage(), e);
        }

        if (token.isCancelled()) return;

        HttpRequest httpReq = HttpRequest.newBuilder()
                .uri(URI.create(url))
                .timeout(timeout)
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofByteArray(body))
                .build();

        HttpResponse<java.io.InputStream> resp;
        try {
            if (!allowPrivate) {
                validateHostAtDialTime(host);
            }
            resp = client.send(httpReq, HttpResponse.BodyHandlers.ofInputStream());
        } catch (Exception e) {
            handleError(output, "request failed: " + e.getMessage(), e);
            return;
        }

        byte[] respBody;
        try (java.io.InputStream is = resp.body()) {
            respBody = readLimited(is, maxResponseSize);
        } catch (Exception e) {
            handleError(output, "response body exceeds " + maxResponseSize + " bytes limit", null);
            return;
        }

        if (resp.statusCode() != 200) {
            handleError(output, "HTTP " + resp.statusCode() + ": " + new String(respBody), null);
            return;
        }

        Map<String, Object> result;
        try {
            result = mapper.readValue(respBody, new TypeReference<>() {});
        } catch (Exception e) {
            throw new PineErrors.OperatorException("transform_by_remote_pineapple: parse response: " + e.getMessage(), e);
        }

        Object errObj = result.get("error");
        if (errObj instanceof String && !((String) errObj).isEmpty()) {
            handleError(output, "downstream error: " + errObj, null);
            return;
        }

        @SuppressWarnings("unchecked")
        Map<String, Object> respCommon = (Map<String, Object>) result.getOrDefault("common", Collections.emptyMap());
        @SuppressWarnings("unchecked")
        List<Map<String, Object>> respItems = (List<Map<String, Object>>) result.getOrDefault("items", Collections.emptyList());

        for (int i = 0; i < commonOutput.size() && i < cResp.size(); i++) {
            String remoteField = cResp.get(i);
            if (respCommon.containsKey(remoteField)) {
                output.setCommon(commonOutput.get(i), respCommon.get(remoteField));
            }
        }

        for (int j = 0; j < input.itemCount() && j < respItems.size(); j++) {
            Map<String, Object> respItem = respItems.get(j);
            for (int i = 0; i < itemOutput.size() && i < iResp.size(); i++) {
                String remoteField = iResp.get(i);
                if (respItem.containsKey(remoteField)) {
                    output.setItem(j, itemOutput.get(i), respItem.get(remoteField));
                }
            }
        }
    }

    private void handleError(OperatorOutput output, String msg, Exception cause) throws PineErrors.OperatorException {
        String fullMsg = "transform_by_remote_pineapple: " + msg;
        if (failOnError) {
            throw new PineErrors.OperatorException(fullMsg, cause);
        }
        output.setWarning(new RuntimeException(fullMsg, cause));
    }

    private static void validateHost(String host) {
        if (host.isEmpty() || "localhost".equals(host)) {
            throw new IllegalArgumentException("transform_by_remote_pineapple: host \"" + host + "\" is not allowed (private/loopback)");
        }
        try {
            InetAddress[] addrs = InetAddress.getAllByName(host);
            for (InetAddress addr : addrs) {
                if (isPrivateAddress(addr)) {
                    throw new IllegalArgumentException("transform_by_remote_pineapple: host \"" + host + "\" resolves to private address " + addr.getHostAddress());
                }
            }
        } catch (UnknownHostException e) {
            // DNS may not be available at init; dial-time check is the real guard
        }
    }

    private static void validateHostAtDialTime(String host) throws Exception {
        InetAddress[] addrs = InetAddress.getAllByName(host);
        for (InetAddress addr : addrs) {
            if (isPrivateAddress(addr)) {
                throw new SecurityException("transform_by_remote_pineapple: dial-time SSRF check failed: \"" + host + "\" resolves to private address " + addr.getHostAddress());
            }
        }
    }

    private static boolean isPrivateAddress(InetAddress addr) {
        return addr.isLoopbackAddress() || addr.isSiteLocalAddress() || addr.isLinkLocalAddress();
    }

    @SuppressWarnings("unchecked")
    private static List<String> toStringList(Object v) {
        if (v instanceof List) {
            List<String> result = new ArrayList<>();
            for (Object elem : (List<?>) v) {
                result.add(String.valueOf(elem));
            }
            return result;
        }
        return Collections.emptyList();
    }

    private static long toLong(Object v) {
        if (v instanceof Number) return ((Number) v).longValue();
        return 0;
    }

    private static byte[] readLimited(java.io.InputStream in, long limit) throws Exception {
        java.io.ByteArrayOutputStream buf = new java.io.ByteArrayOutputStream();
        byte[] tmp = new byte[8192];
        long total = 0;
        int n;
        while ((n = in.read(tmp)) != -1) {
            total += n;
            if (total > limit) {
                throw new RuntimeException("response too large");
            }
            buf.write(tmp, 0, n);
        }
        return buf.toByteArray();
    }
}
