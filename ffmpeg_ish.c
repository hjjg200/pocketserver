
#include "ffmpeg_ish.h"

#include <stdio.h>

// Execute ffmpeg and capture output
int execute_ffmpeg(const char *cmd, char *output, size_t output_size) {

    // Redirect stderr to stdout using "2>&1"
    char full_cmd[1024];
    snprintf(full_cmd, sizeof(full_cmd), "%s 2>&1", cmd);

    FILE *fp = popen(full_cmd, "r");
    if (fp == NULL) {
        return -1; // Failed to execute
    }

    // Read the output
    size_t len = 0;
    while (fgets(output + len, output_size - len, fp) != NULL) {
        len += strlen(output + len);
        if (len >= output_size - 1) break; // Prevent overflow
    }

    int status = pclose(fp);

    return WEXITSTATUS(status); // Return the exit status of ffmpeg
}
