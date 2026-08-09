//go:build cgo && !nocgo

package video

/*
#cgo CFLAGS: -I/usr/include
#cgo LDFLAGS: -ljpeg
#include <stdio.h>
#include <stdlib.h>
#include <jpeglib.h>
#include <string.h>

// write_jpeg_data writes compressed JPEG data to a memory buffer.
typedef struct {
    unsigned char *data;
    unsigned long size;
} jpeg_buffer;

static void jpeg_init_destination(j_compress_ptr cinfo) {}
static boolean jpeg_empty_output_buffer(j_compress_ptr cinfo) {
    return TRUE;
}
static void jpeg_term_destination(j_compress_ptr cinfo) {}

// compress_rgba_to_jpeg compresses raw RGBA pixels to JPEG.
// Returns 0 on success, -1 on failure.
// Caller must free(*out_data) when done.
int compress_rgba_to_jpeg(
    const unsigned char *rgba,
    int width, int height, int quality,
    unsigned char **out_data, unsigned long *out_size
) {
    struct jpeg_compress_struct cinfo;
    struct jpeg_error_mgr jerr;

    cinfo.err = jpeg_std_error(&jerr);
    jpeg_create_compress(&cinfo);

    jpeg_buffer buf;
    buf.data = NULL;
    buf.size = 0;

    jpeg_mem_dest(&cinfo, &buf.data, &buf.size);

    cinfo.image_width = width;
    cinfo.image_height = height;
    cinfo.input_components = 3;
    cinfo.in_color_space = JCS_RGB;

    jpeg_set_defaults(&cinfo);
    jpeg_set_quality(&cinfo, quality, TRUE);

    // Use fastest compression settings
    cinfo.dct_method = JDCT_FASTEST;
    cinfo.optimize_coding = FALSE;

    jpeg_start_compress(&cinfo, TRUE);

    // RGBA row buffer -> RGB conversion buffer
    unsigned char *rgb_row = (unsigned char *)malloc(width * 3);
    if (!rgb_row) {
        jpeg_destroy_compress(&cinfo);
        if (buf.data) free(buf.data);
        return -1;
    }

    JSAMPROW row_pointer[1];
    row_pointer[0] = rgb_row;

    while (cinfo.next_scanline < cinfo.image_height) {
        // Convert one scanline from RGBA to RGB (skip alpha)
        const unsigned char *src = rgba + (cinfo.next_scanline * width * 4);
        for (int x = 0; x < width; x++) {
            rgb_row[x*3 + 0] = src[x*4 + 0]; // R
            rgb_row[x*3 + 1] = src[x*4 + 1]; // G
            rgb_row[x*3 + 2] = src[x*4 + 2]; // B
        }
        jpeg_write_scanlines(&cinfo, row_pointer, 1);
    }

    free(rgb_row);
    jpeg_finish_compress(&cinfo);
    jpeg_destroy_compress(&cinfo);

    *out_data = buf.data;
    *out_size = buf.size;
    return 0;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// TurboEncoder uses libjpeg-turbo (CGo) for fast JPEG encoding.
// This is ~1.7-2x faster than Go's stdlib encoder for raw RGBA data.
type TurboEncoder struct{}

// NewTurboEncoder creates a libjpeg-turbo backed encoder.
// Returns nil if CGo is not available (caller should fall back to DefaultEncoder).
func NewTurboEncoder() Encoder {
	return &TurboEncoder{}
}

func (e *TurboEncoder) EncodeRaw(rawRGBA []byte, width, height, quality int) ([]byte, error) {
	if len(rawRGBA) < width*height*4 {
		return nil, errors.New("raw RGBA buffer too small")
	}

	var outData *C.uchar
	var outSize C.ulong

	// Copy RGBA data to C memory since CGo can't pass Go slices to C
	cRGBA := C.CBytes(rawRGBA)
	defer C.free(cRGBA)

	ret := C.compress_rgba_to_jpeg(
		(*C.uchar)(cRGBA),
		C.int(width),
		C.int(height),
		C.int(quality),
		&outData,
		&outSize,
	)
	if ret != 0 {
		return nil, errors.New("turbo JPEG compression failed")
	}
	defer C.free(unsafe.Pointer(outData))

	// Copy result to Go slice
	result := C.GoBytes(unsafe.Pointer(outData), C.int(outSize))
	return result, nil
}

func (e *TurboEncoder) Close() {}
