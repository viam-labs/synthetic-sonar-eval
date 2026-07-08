package detector

import (
	"image"
	"math"
	"math/rand"

	xdraw "golang.org/x/image/draw"
)

// backgroundStrip mirrors OmniOnnxInference._background_strip_np: it
// estimates the dominant background color via k-means on a 100x100
// downsample of src, then zeroes out (to black) every pixel within
// Euclidean distance dist (8-bit RGB space) of that color, returning a new
// image the same size as src.
func backgroundStrip(src *image.RGBA, dist float64) *image.RGBA {
	bg := estimateBackgroundColor(src)
	distSq := dist * dist

	b := src.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			si := src.PixOffset(x, y)
			oi := out.PixOffset(x, y)
			r, g, bl := float64(src.Pix[si]), float64(src.Pix[si+1]), float64(src.Pix[si+2])
			dr, dg, db := r-bg[0], g-bg[1], bl-bg[2]
			if dr*dr+dg*dg+db*db <= distSq {
				out.Pix[oi+3] = 255 // r, g, b already zero
			} else {
				copy(out.Pix[oi:oi+4], src.Pix[si:si+4])
			}
		}
	}
	return out
}

// estimateBackgroundColor mirrors get_background_from_img_tensor: resize to
// 100x100, k-means cluster into 5 groups, and return the centroid of the
// largest cluster (0-255 RGB) as the background color. This is a functional
// port, not a bit-exact one — it uses k-means++ initialization with a few
// restarts rather than cv2.kmeans's KMEANS_RANDOM_CENTERS, which is close
// enough for estimating a single dominant background color.
func estimateBackgroundColor(src image.Image) [3]float64 {
	const kmeansSize = 100
	small := image.NewRGBA(image.Rect(0, 0, kmeansSize, kmeansSize))
	xdraw.BiLinear.Scale(small, small.Bounds(), src, src.Bounds(), xdraw.Src, nil)

	points := make([][3]float64, 0, kmeansSize*kmeansSize)
	for y := 0; y < kmeansSize; y++ {
		for x := 0; x < kmeansSize; x++ {
			i := small.PixOffset(x, y)
			points = append(points, [3]float64{
				float64(small.Pix[i+0]),
				float64(small.Pix[i+1]),
				float64(small.Pix[i+2]),
			})
		}
	}

	const k = 5
	centers, assignments := kmeans(points, k, 50, 4)

	counts := make([]int, len(centers))
	for _, a := range assignments {
		counts[a]++
	}
	maxLabel := 0
	for i, c := range counts {
		if c > counts[maxLabel] {
			maxLabel = i
		}
	}
	return centers[maxLabel]
}

// kmeans clusters points into k groups, running `attempts` independent
// restarts (k-means++ initialization) and keeping the lowest-inertia
// result, similar in spirit to cv2.kmeans(..., attempts=N).
func kmeans(points [][3]float64, k, maxIters, attempts int) ([][3]float64, []int) {
	var bestCenters [][3]float64
	var bestAssign []int
	bestInertia := math.Inf(1)

	for a := 0; a < attempts; a++ {
		centers := kmeansPlusPlusInit(points, k)
		assign := make([]int, len(points))
		for iter := 0; iter < maxIters; iter++ {
			changed := false
			for i, p := range points {
				best, bestD := 0, math.Inf(1)
				for c, center := range centers {
					if d := sqDist(p, center); d < bestD {
						bestD, best = d, c
					}
				}
				if assign[i] != best {
					assign[i] = best
					changed = true
				}
			}

			sums := make([][3]float64, k)
			counts := make([]int, k)
			for i, p := range points {
				c := assign[i]
				sums[c][0] += p[0]
				sums[c][1] += p[1]
				sums[c][2] += p[2]
				counts[c]++
			}
			for c := range centers {
				if counts[c] == 0 {
					continue
				}
				centers[c] = [3]float64{
					sums[c][0] / float64(counts[c]),
					sums[c][1] / float64(counts[c]),
					sums[c][2] / float64(counts[c]),
				}
			}
			if !changed {
				break
			}
		}

		inertia := 0.0
		for i, p := range points {
			inertia += sqDist(p, centers[assign[i]])
		}
		if inertia < bestInertia {
			bestInertia, bestCenters, bestAssign = inertia, centers, assign
		}
	}
	return bestCenters, bestAssign
}

// kmeansPlusPlusInit picks k initial centers from points using k-means++
// weighted sampling (each subsequent center is chosen with probability
// proportional to its squared distance from the nearest existing center).
func kmeansPlusPlusInit(points [][3]float64, k int) [][3]float64 {
	centers := make([][3]float64, 0, k)
	centers = append(centers, points[rand.Intn(len(points))])
	for len(centers) < k {
		distSq := make([]float64, len(points))
		total := 0.0
		for i, p := range points {
			minD := math.Inf(1)
			for _, c := range centers {
				if d := sqDist(p, c); d < minD {
					minD = d
				}
			}
			distSq[i] = minD
			total += minD
		}
		if total == 0 {
			centers = append(centers, points[rand.Intn(len(points))])
			continue
		}
		r := rand.Float64() * total
		acc := 0.0
		chosen := len(points) - 1 // fallback for floating-point rounding
		for i, d := range distSq {
			acc += d
			if acc >= r {
				chosen = i
				break
			}
		}
		centers = append(centers, points[chosen])
	}
	return centers
}

func sqDist(a, b [3]float64) float64 {
	dr, dg, db := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return dr*dr + dg*dg + db*db
}
