package pricing

import "math"

func SaleFen(upstreamUSD, rate, markupCNY float64) int64 {
	if upstreamUSD < 0 || rate <= 0 || markupCNY < 0 {
		panic("invalid pricing configuration")
	}
	return int64(math.Ceil(upstreamUSD*rate*100)) + int64(math.Round(markupCNY*100))
}
