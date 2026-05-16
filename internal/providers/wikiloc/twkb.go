package wikiloc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"wanderer-import/internal/providers/providerkit"
)

func ParseTWKBLineString(data []byte) ([]providerkit.Point, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("twkb too short")
	}
	buf := bytes.NewReader(data)

	// Byte 0: type_and_prec
	b0, _ := buf.ReadByte()
	geomType := b0 & 15
	if geomType != 2 { // 2 = LineString
		return nil, fmt.Errorf("unsupported twkb geom type: %d", geomType)
	}

	precEnc := (b0 & 240) >> 4
	prec := int(-(precEnc & 1)) ^ int(precEnc>>1)

	// Byte 1: metadata
	b1, _ := buf.ReadByte()
	hasBbox := (b1 & 1) != 0
	hasSize := (b1 & 2) != 0
	// hasIdList := (b1 & 4) != 0
	hasExt := (b1 & 8) != 0
	isEmpty := (b1 & 16) != 0

	if isEmpty {
		return nil, nil
	}

	hasZ := false
	hasM := false
	zPrec := 0
	// mPrec := 0

	if hasExt {
		b2, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		hasZ = (b2 & 1) != 0
		if hasZ {
			zPrec = int((b2 & 28) >> 2)
		}
		hasM = (b2 & 2) != 0
		// if hasM {
		// 	mPrec = int((b2 & 224) >> 5)
		// }
	}

	if hasSize {
		if _, err := binary.ReadUvarint(buf); err != nil {
			return nil, err
		}
	}

	nDims := 2
	if hasZ {
		nDims++
	}
	if hasM {
		nDims++
	}

	if hasBbox {
		for i := 0; i < nDims*2; i++ {
			if _, err := binary.ReadVarint(buf); err != nil {
				return nil, err
			}
		}
	}

	numPoints, err := binary.ReadUvarint(buf)
	if err != nil {
		return nil, err
	}

	factorXY := math.Pow(10, float64(-prec))
	factorZ := 1.0
	if hasZ {
		factorZ = math.Pow(10, float64(-zPrec))
	}

	var points []providerkit.Point
	vals := make([]int64, nDims)

	for i := 0; uint64(i) < numPoints; i++ {
		dim := 0

		// X (Lon)
		deltaX, err := binary.ReadVarint(buf)
		if err != nil {
			return nil, err
		}
		vals[dim] += deltaX
		x := float64(vals[dim]) * factorXY
		dim++

		// Y (Lat)
		deltaY, err := binary.ReadVarint(buf)
		if err != nil {
			return nil, err
		}
		vals[dim] += deltaY
		y := float64(vals[dim]) * factorXY
		dim++

		var ele *float64
		if hasZ {
			deltaZ, err := binary.ReadVarint(buf)
			if err != nil {
				return nil, err
			}
			vals[dim] += deltaZ
			z := float64(vals[dim]) * factorZ
			ele = &z
			dim++
		}

		if hasM {
			deltaM, err := binary.ReadVarint(buf)
			if err != nil {
				return nil, err
			}
			vals[dim] += deltaM
			dim++
		}

		points = append(points, providerkit.Point{Lon: x, Lat: y, Ele: ele})
	}

	return points, nil
}
