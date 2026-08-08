package page.liam.pine.metrics;

/**
 * Creates typed metrics, registering them with a backend inside these factory
 * methods.
 *
 * <p><b>Implementer's contract:</b> the authoritative copy lives in
 * {@code pine-go/pkg/metrics/metrics.go} (package doc, "Implementer's
 * contract"). This file aligns to it and deliberately does not restate it, so
 * the two cannot drift. Read it before implementing Provider — it covers
 * concurrency, units, buckets-as-suggestion and label-value lifetime, none of
 * which are inferable from the signatures here.
 *
 * <p>The one point repeated because it affects correctness rather than
 * accuracy: <b>every method on the returned metrics is called
 * concurrently</b>. Pine's scheduler runs independent operators in parallel, so
 * implementations must be thread-safe. The bundled {@link MetricsCollector} and
 * {@link NopProvider} both are, which means a non-thread-safe Provider will not
 * be caught by pine's own tests.
 */
public interface Provider {
    Counter newCounter(MetricOpts opts);
    Gauge newGauge(MetricOpts opts);
    Histogram newHistogram(HistogramOpts opts);
}
