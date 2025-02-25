#ifndef IO_ISH_H
#define IO_ISH_H

#define IO_ISH_SUPERVISOR "[SUPERVISOR]"


#include <stdlib.h>
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>
#include <dirent.h>
#include <errno.h>
#include <poll.h>
#include <signal.h>
//#include <sys/reboot.h>

void reset_errno();
int get_errno();
int my_open(const char *pathname, int flags, mode_t mode);
int waitFD(int fd, short events, int timeout);
int fork_supervisor(int spawnerPid);

#endif // IO_ISH_H