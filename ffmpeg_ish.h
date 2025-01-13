#ifndef FFMPEG_ISH_H
#define FFMPEG_ISH_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int execute_ffmpeg(const char *cmd, char *output, size_t output_size);

#endif // FFMPEG_ISH_H