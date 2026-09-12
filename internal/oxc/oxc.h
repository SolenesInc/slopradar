#ifndef SLOPRADAR_OXC_H
#define SLOPRADAR_OXC_H

#include <stddef.h>
#include <stdint.h>

typedef struct {
    uint8_t *data;
    size_t len;
    size_t capacity;
} slopradar_oxc_buffer;

int32_t slopradar_oxc_analyze(
    const uint8_t *file_data,
    size_t file_len,
    const uint8_t *source_data,
    size_t source_len,
    slopradar_oxc_buffer *output
);

void slopradar_oxc_release(slopradar_oxc_buffer *buffer);

#endif
