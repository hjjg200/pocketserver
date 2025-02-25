// +build linux,386
// linux and 386
// build for iSH

#include "io_ish.h"


void reset_errno() {
    errno = 0;
}

int get_errno() {
    return errno;
}

// Fixed-signature wrapper for open.
int my_open(const char *pathname, int flags, mode_t mode) {
    return open(pathname, flags, mode);
}

// waitFD waits for the file descriptor fd to become ready for the given events (e.g. POLLIN, POLLOUT)
// within the timeout (in milliseconds). It loops on EINTR.
int waitFD(int fd, short events, int timeout) {
    struct pollfd pfd;
    pfd.fd = fd;
    pfd.events = events;
    int ret;
    do {
        ret = poll(&pfd, 1, timeout);
    } while(ret == -1 && errno == EINTR);
    return ret;
}

//
int fork_supervisor(int spawnerPid) {

    // Save master stdout/stderr fds at the top.
    int masterStdout = STDOUT_FILENO;
    int masterStderr = STDERR_FILENO;

    // In the child, masterPid is parent's pid.
    // In the parent, getpid() is the master pid.
    // but for heartbeat we'll create a new pipe.
    int fds[2];
    if (pipe(fds) < 0) {
        perror(IO_ISH_SUPERVISOR "pipe");
        return -1;
    }
    
    pid_t pid = fork();
    if (pid < 0) {
        perror(IO_ISH_SUPERVISOR "fork failed");
        return -1;
    }
    
    if (pid == 0) {

        if (dup2(masterStdout, STDOUT_FILENO) == -1) {
            perror(IO_ISH_SUPERVISOR "dup2 stdout failed");
            _exit(1); // use _exit() in child
        }

        if (dup2(masterStderr, STDERR_FILENO) == -1) {
            perror(IO_ISH_SUPERVISOR "dup2 stderr failed");
            _exit(1); // use _exit() in child
        }

        /*
        Doing SIGKILL on frozen go app or reboot doesn't work on iSH

        SIGKILL DOESN'T WORK
        // Child process: this is the supervisor.
        // Get the master's pid (the parent's pid)
        int masterPid = getppid();
        fprintf(stderr, "Timeout: no heartbeat in 5 seconds. Killing master (pid %d).\n", masterPid);
        kill(masterPid, SIGKILL);

        REBOOT NOT WORKING
        // Flush filesystem buffers.
        sync();
        
        // Initiate a reboot (RB_AUTOBOOT reboots the system).
        if (reboot(RB_AUTOBOOT) < 0) {
            perror("reboot failed");
            _exit(1);
        }
        */
        // Close the write end; only read heartbeats.
        close(fds[1]);
        
        char buf[16];
        while (1) {
            struct pollfd pfd;
            pfd.fd = fds[0];
            pfd.events = POLLIN;
            
            // Wait up to 5000ms (5 seconds) for data.
            int ret = poll(&pfd, 1, 5000);
            if (ret == 0) {
                // Timeout: no heartbeat received.
                fprintf(stderr, IO_ISH_SUPERVISOR "Timeout: no heartbeat in 5 seconds.\n");

                // Does SIGKILL on spawner
                // if pocketserver_ish is configured to run using .profile
                /*
                # .profile
                (while true; do
                    (pocketserver_ish -termSpawnerOnHang)
                    sleep 1
                done) &
                */
                // This can effectively workaround the hang of the go app
                // with a drawback that there will be growing locked memory over time
                if (spawnerPid > 0) {
                    fprintf(stderr, IO_ISH_SUPERVISOR "SIGKILL-ing spawner (pid %d).\n", spawnerPid);
                    kill(spawnerPid, SIGKILL);
                }

                _exit(1);
            } else if (ret < 0) {
                perror(IO_ISH_SUPERVISOR "poll error");
                _exit(1);
            }
            
            // Data is available.
            ssize_t n = read(fds[0], buf, sizeof(buf) - 1);
            if (n == 0) {
                // EOF: parent's write end closed.
                fprintf(stderr, IO_ISH_SUPERVISOR "Master is not alive (pipe closed).\n");
                _exit(1);
            } else if (n < 0) {
                perror(IO_ISH_SUPERVISOR "read error");
                _exit(1);
            } else {
                //buf[n] = '\0'; // null-terminate the string
                //printf("Child received heartbeat: %s\n", buf);
            }
        }
    }
    
    // Parent process (the master)
    // Close the read end; we'll use the write end to send heartbeats.
    close(fds[0]);
    
    // Return the write fd so that the master can write heartbeat messages.
    return fds[1];
}
