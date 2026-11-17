/*
 * tapwait: wait for one tap on the Kindle touchscreen, without the reader seeing it.
 *
 * Grabs the touch device (EVIOCGRAB) so the tap doesn't also turn the page, maps touch
 * coordinates to screen pixels using the device's axis ranges, and prints the name of the
 * region the finger was lifted in. Taps outside every region are ignored.
 *
 * usage: tapwait <device|auto> <screen_w> <screen_h> <timeout_s> <swap> <mirror_x> <mirror_y> <orientation> name:x0,y0,x1,y1 [...]
 *   device "auto" picks the first /dev/input/event* that reports multitouch (or single touch) axes.
 *   swap/mirror_x/mirror_y are 0 or 1, from FBInk's touchSwapAxes/touchMirrorX/touchMirrorY for the model.
 *   orientation is the window manager's U, R, L or D: the touch panel stays in portrait when the screen turns.
 * prints: <name> | timeout | error:<msg>
 */
#include <errno.h>
#include <fcntl.h>
#include <linux/input.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <time.h>
#include <unistd.h>

typedef struct { char name[32]; int x0, y0, x1, y1; } region_t;

static long now_ms(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec * 1000L + ts.tv_nsec / 1000000L;
}

int main(int argc, char **argv) {
	if (argc < 10) { puts("error:usage"); return 2; }
	const char *dev = argv[1];
	int sw = atoi(argv[2]), sh = atoi(argv[3]);
	long timeout = atol(argv[4]) * 1000L;
	int swap = atoi(argv[5]), mirx = atoi(argv[6]), miry = atoi(argv[7]);
	char orient = argv[8][0];
	int first = 9;
	int n = argc - first;
	region_t r[16];
	if (n > 16) n = 16;
	for (int i = 0; i < n; i++) {
		char *p = strchr(argv[first + i], ':');
		if (!p) { puts("error:region"); return 2; }
		size_t len = (size_t)(p - argv[first + i]);
		if (len > 31) len = 31;
		memcpy(r[i].name, argv[first + i], len);
		r[i].name[len] = 0;
		if (sscanf(p + 1, "%d,%d,%d,%d", &r[i].x0, &r[i].y0, &r[i].x1, &r[i].y1) != 4) { puts("error:region"); return 2; }
	}

	int fd = -1;
	char path[64];
	if (strcmp(dev, "auto") == 0) {
		for (int i = 0; i < 16 && fd < 0; i++) {
			snprintf(path, sizeof path, "/dev/input/event%d", i);
			int t = open(path, O_RDONLY | O_NONBLOCK);
			if (t < 0) continue;
			struct input_absinfo a = {0};
			if ((ioctl(t, EVIOCGABS(ABS_MT_POSITION_X), &a) == 0 && a.maximum > 0) ||
			    (ioctl(t, EVIOCGABS(ABS_X), &a) == 0 && a.maximum > 0)) { fd = t; dev = path; }
			else close(t);
		}
		if (fd < 0) { puts("error:no touchscreen found"); return 1; }
	} else {
		fd = open(dev, O_RDONLY | O_NONBLOCK);
		if (fd < 0) { printf("error:open %s\n", strerror(errno)); return 1; }
	}

	struct input_absinfo ax = {0}, ay = {0};
	int xcode = ABS_MT_POSITION_X, ycode = ABS_MT_POSITION_Y;
	if (ioctl(fd, EVIOCGABS(ABS_MT_POSITION_X), &ax) < 0 || ax.maximum <= 0) {
		xcode = ABS_X; ycode = ABS_Y;
		ioctl(fd, EVIOCGABS(ABS_X), &ax);
	}
	ioctl(fd, EVIOCGABS(ycode), &ay);
	if (ax.maximum <= ax.minimum || ay.maximum <= ay.minimum) { puts("error:absinfo"); close(fd); return 1; }

	if (ioctl(fd, EVIOCGRAB, 1) < 0) { printf("error:grab %s\n", strerror(errno)); close(fd); return 1; }

	/* Drain anything already queued so a finger lifting from the previous tap isn't counted */
	struct input_event ev;
	while (read(fd, &ev, sizeof ev) == sizeof ev) {}

	int x = -1, y = -1, down = 0;
	const char *result = "timeout";
	long deadline = now_ms() + timeout;
	while (1) {
		long left = deadline - now_ms();
		if (left <= 0) break;
		struct pollfd pfd = { fd, POLLIN, 0 };
		if (poll(&pfd, 1, (int)left) <= 0) continue;
		int lifted = 0;
		while (read(fd, &ev, sizeof ev) == sizeof ev) {
			if (ev.type == EV_ABS && ev.code == xcode) x = ev.value;
			else if (ev.type == EV_ABS && ev.code == ycode) y = ev.value;
			else if (ev.type == EV_ABS && ev.code == ABS_MT_TRACKING_ID) { if (ev.value >= 0) down = 1; else if (down) { down = 0; lifted = 1; } }
			else if (ev.type == EV_KEY && ev.code == BTN_TOUCH) { if (ev.value) down = 1; else if (down) { down = 0; lifted = 1; } }
		}
		if (lifted && x >= 0 && y >= 0) {
			/* Normalise to 0..1 on each touch axis, then apply the model's swap/mirror quirks */
			double fx = (double)(x - ax.minimum) / (ax.maximum - ax.minimum + 1);
			double fy = (double)(y - ay.minimum) / (ay.maximum - ay.minimum + 1);
			if (swap) { double t = fx; fx = fy; fy = t; }
			if (mirx) fx = 1.0 - fx;
			if (miry) fy = 1.0 - fy;
			double t = fx;
			switch (orient) {
			case 'R': fx = 1.0 - fy; fy = t; break;
			case 'L': fx = fy; fy = 1.0 - t; break;
			case 'D': fx = 1.0 - fx; fy = 1.0 - fy; break;
			}
			int px = (int)(fx * sw), py = (int)(fy * sh);
			fprintf(stderr, "tap raw=%d,%d px=%d,%d\n", x, y, px, py);
			for (int i = 0; i < n; i++) {
				if (px >= r[i].x0 && px <= r[i].x1 && py >= r[i].y0 && py <= r[i].y1) { result = r[i].name; goto done; }
			}
		}
	}
done:
	ioctl(fd, EVIOCGRAB, 0);
	close(fd);
	puts(result);
	return 0;
}
