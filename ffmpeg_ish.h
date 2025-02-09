#ifndef FFMPEG_ISH_H
#define FFMPEG_ISH_H

#include <stddef.h>
#include <sys/types.h> // for pid_t

int execute_ffmpeg_popen(const char *cmd, char *output, size_t output_size);
pid_t start_ffmpeg(char *const args[], int stdout_fd, int stderr_fd);
int wait_process(pid_t pid);
int terminate_process(pid_t pid, int force);

#endif // FFMPEG_ISH_H