#ifndef CAPTURE_DARWIN_H
#define CAPTURE_DARWIN_H

#include <stdint.h>
#include <stddef.h>

enum {
	CAP_OK = 0,
	CAP_ERR_GENERIC = -1,
	CAP_ERR_PERMISSION = -2,
	CAP_ERR_UNSUPPORTED = -3,
	CAP_ERR_NO_DEVICE = -4,
};

// Clock & system checks.
int64_t cap_host_100ns(void);
int cap_macos_version_ok(int *major, int *minor);
int cap_is_app_bundle(void);
int cap_has_screen_permission(void);
int cap_request_screen_permission(void);
int cap_camera_auth_status(int *has_usage_desc);
int cap_request_camera_permission(void);
int cap_mic_auth_status(int *has_usage_desc);
int cap_request_mic_permission(void);

// Displays.
typedef struct {
	uint32_t display_id;
	int x, y, width, height;
	double bounds_x, bounds_y, bounds_w, bounds_h;
	double scale;
	int primary;
	char name[128];
} cap_display_info;

int cap_list_displays(cap_display_info *out, int max_count);

// Audio device enumeration.
typedef struct {
	char id[256];
	char name[256];
	int is_default;
} cap_audio_device_info;

int cap_list_mics(cap_audio_device_info *out, int max_count);
int cap_default_mic_uid(char *out, int out_len);

// Camera enumeration.
typedef struct {
	char id[256];
	char name[256];
} cap_camera_info;

int cap_list_cameras(cap_camera_info *out, int max_count);

// Screenshot (writes opaque RGBA pixels into rgba_out of size width*height*4).
int cap_grab_screenshot(uint32_t display_id, double src_x, double src_y, double src_w, double src_h,
	int out_w, int out_h, int cursor, int flush, uint8_t *rgba_out, char *err_buf, int err_len);

// Screen video stream (delivers NV12 frames to goScreenFrame).
void *cap_screen_start(uint32_t display_id, double src_x, double src_y, double src_w, double src_h,
	int out_w, int out_h, int fps, int cursor, uintptr_t handle, int *err_code, char *err_buf, int err_len);
void cap_screen_stop(void *ctx);

// System audio capture via ScreenCaptureKit (delivers 48kHz stereo float32 to goAudioSamples).
void *cap_sysaudio_start(uintptr_t handle, int *err_code, char *err_buf, int err_len);
void cap_sysaudio_stop(void *ctx);

// Microphone capture via CoreAudio AudioQueue (delivers 48kHz stereo float32 to goAudioSamples).
void *cap_mic_start(const char *uid, uintptr_t handle, int *err_code, char *err_buf, int err_len);
void cap_mic_stop(void *ctx);

// Camera capture via AVFoundation + Accelerate/vImage (delivers RGBA frames to goCameraFrame).
void *cap_camera_start(const char *uid, int width, int height, int mirror, uintptr_t handle,
	int *err_code, char *err_buf, int err_len);
void cap_camera_stop(void *ctx);

#endif
