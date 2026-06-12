package metrics

// Nop returns a Provider that discards all observations at zero cost.
func Nop() Provider { return nopProvider{} }

type nopProvider struct{}

func (nopProvider) NewCounter(MetricOpts) Counter        { return nopCounter{} }
func (nopProvider) NewGauge(MetricOpts) Gauge            { return nopGauge{} }
func (nopProvider) NewHistogram(HistogramOpts) Histogram { return nopHistogram{} }

type nopCounter struct{}

func (nopCounter) With(...string) Counter { return nopCounter{} }
func (nopCounter) Inc()                   {}

type nopGauge struct{}

func (nopGauge) With(...string) Gauge { return nopGauge{} }
func (nopGauge) Set(float64)          {}
func (nopGauge) Add(float64)          {}

type nopHistogram struct{}

func (nopHistogram) With(...string) Histogram { return nopHistogram{} }
func (nopHistogram) Observe(float64)          {}
