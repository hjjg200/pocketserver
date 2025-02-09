// +build linux,386
// linux and 386
// build for iSH

#include "ffmpeg_ish.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/wait.h>
#include <string.h>
#include <sys/types.h> // for pid_t
#include <signal.h>


// Execute ffmpeg and capture output
int execute_ffmpeg_popen(const char *cmd, char *output, size_t output_size) {

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



// Start ffmpeg (or another program) with the given command string and optional
// stdout/stderr redirection. Returns the child's PID on success, or -1 on error.
pid_t start_ffmpeg(char *const args[], int stdout_fd, int stderr_fd) {
    // Fork a new process
    pid_t pid = fork();
    if (pid < 0) {
        perror("fork failed");
        return -1;
    }

    if (pid == 0) {
        // Child process
        if (stdout_fd != -1) {
            if (dup2(stdout_fd, STDOUT_FILENO) == -1) {
                perror("dup2 stdout failed");
                _exit(1); // use _exit() in child
            }
        }

        if (stderr_fd != -1) {
            if (dup2(stderr_fd, STDERR_FILENO) == -1) {
                perror("dup2 stderr failed");
                _exit(1); // use _exit() in child
            }
        }

        /**
        // Tokenize the cmd string into args for execvp()
        char *args[128];
        char cmd_copy[4096];
        snprintf(cmd_copy, sizeof(cmd_copy), "%s", cmd);

        int i = 0;
        char *token = strtok(cmd_copy, " ");
        while (token != NULL && i < 127) {
            args[i++] = token;
            token = strtok(NULL, " ");
        }
        args[i] = NULL;  // Null-terminate the argument list

        // Execute the command
        execvp(args[0], args);
         */

        execvp(args[0], args);

        // If execvp() fails:
        perror("execvp failed");
        _exit(1);
    }

    // Parent process: return child's PID
    return pid;
}

// Waits for the child process with the given pid. Returns:
//  - the child's exit code if it exited normally
//  - 128 + signal number if it was signaled
//  - -1 on error or unusual cases
int wait_process(pid_t pid) {
    int status;
    pid_t w = waitpid(pid, &status, 0);
    if (w < 0) {
        perror("waitpid failed");
        return -1;
    }

    if (WIFEXITED(status)) {
        return WEXITSTATUS(status);
    }
    if (WIFSIGNALED(status)) {
        return 128 + WTERMSIG(status);
    }
    return -1; // e.g., stopped, etc.
}

// Sends SIGTERM (graceful) or SIGKILL (force) to a process
int terminate_process(pid_t pid, int force) {
    if (pid <= 0) {
        fprintf(stderr, "Invalid PID\n");
        return -1;
    }

    int signal = force ? SIGKILL : SIGTERM;
    if (kill(pid, signal) == -1) {
        perror("kill failed");
        return -1;
    }

    return 0; // Success
}