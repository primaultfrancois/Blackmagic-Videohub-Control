#ifndef VH_WEBVIEW_H
#define VH_WEBVIEW_H

#include <stdint.h>

// All functions except vh_dispatch and vh_quit must run on the GTK main thread.
// app_id is the Wayland app_id / X11 WM_CLASS; it must match the .desktop file name.
void vh_init(const char *app_id, const char *title, int width, int height, const char *bridge_js, int devtools,
             const void *icon, int icon_len);
void vh_load_html(const char *html, const char *base_uri);
void vh_run(void);
void vh_quit(void);
void vh_eval(const char *js);
void vh_dispatch(uintptr_t id);               // thread-safe: runs goDispatch(id) on the main thread
char *vh_save_dialog(const char *suggested);  // returns a path to free with vh_free, or NULL
void vh_free(void *p);
void vh_set_theme(int dark);                  // 1: dark, 0: light (callable before vh_init)

#endif
