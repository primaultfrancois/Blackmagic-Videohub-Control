#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

#include "webview.h"
#include "_cgo_export.h"

static GtkWidget *window;
static WebKitWebView *view;

static void on_script_message(WebKitUserContentManager *manager, WebKitJavascriptResult *result, gpointer data) {
    JSCValue *value = webkit_javascript_result_get_js_value(result);
    char *msg = jsc_value_to_string(value);
    goOnMessage(msg);
    g_free(msg);
}

// Open external links in the system browser instead of inside the app.
static gboolean on_decide_policy(WebKitWebView *v, WebKitPolicyDecision *decision,
                                 WebKitPolicyDecisionType type, gpointer data) {
    if (type != WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION && type != WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION)
        return FALSE;
    WebKitNavigationAction *action =
        webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(decision));
    const char *uri = webkit_uri_request_get_uri(webkit_navigation_action_get_request(action));
    if (uri && (g_str_has_prefix(uri, "http://") || g_str_has_prefix(uri, "https://")) &&
        webkit_navigation_action_is_user_gesture(action)) {
        gtk_show_uri_on_window(GTK_WINDOW(window), uri, GDK_CURRENT_TIME, NULL);
        webkit_policy_decision_ignore(decision);
        return TRUE;
    }
    return FALSE;
}

void vh_init(const char *title, int width, int height, const char *bridge_js, int devtools) {
    gtk_init(NULL, NULL);

    window = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_title(GTK_WINDOW(window), title);
    gtk_window_set_default_size(GTK_WINDOW(window), width, height);
    gtk_window_set_icon_name(GTK_WINDOW(window), "video-display");
    g_signal_connect(window, "destroy", G_CALLBACK(gtk_main_quit), NULL);

    WebKitUserContentManager *ucm = webkit_user_content_manager_new();
    g_signal_connect(ucm, "script-message-received::vh", G_CALLBACK(on_script_message), NULL);
    webkit_user_content_manager_register_script_message_handler(ucm, "vh");
    WebKitUserScript *script = webkit_user_script_new(
        bridge_js, WEBKIT_USER_CONTENT_INJECT_TOP_FRAME, WEBKIT_USER_SCRIPT_INJECT_AT_DOCUMENT_START, NULL, NULL);
    webkit_user_content_manager_add_script(ucm, script);
    webkit_user_script_unref(script);

    view = WEBKIT_WEB_VIEW(webkit_web_view_new_with_user_content_manager(ucm));
    WebKitSettings *settings = webkit_web_view_get_settings(view);
    webkit_settings_set_enable_developer_extras(settings, devtools);
    webkit_settings_set_hardware_acceleration_policy(settings, WEBKIT_HARDWARE_ACCELERATION_POLICY_ALWAYS);
    g_signal_connect(view, "decide-policy", G_CALLBACK(on_decide_policy), NULL);

    // Matches the app's dark chassis colour so there's no white flash at startup.
    GdkRGBA bg = {0.051, 0.055, 0.063, 1.0};
    webkit_web_view_set_background_color(view, &bg);

    gtk_container_add(GTK_CONTAINER(window), GTK_WIDGET(view));
    gtk_widget_show_all(window);
}

void vh_load_html(const char *html, const char *base_uri) {
    webkit_web_view_load_html(view, html, base_uri);
}

void vh_run(void) { gtk_main(); }

static gboolean quit_cb(gpointer data) {
    gtk_main_quit();
    return G_SOURCE_REMOVE;
}

void vh_quit(void) { g_idle_add(quit_cb, NULL); }

void vh_eval(const char *js) {
    webkit_web_view_evaluate_javascript(view, js, -1, NULL, NULL, NULL, NULL, NULL);
}

static gboolean dispatch_cb(gpointer data) {
    goDispatch((uintptr_t)data);
    return G_SOURCE_REMOVE;
}

void vh_dispatch(uintptr_t id) { g_idle_add(dispatch_cb, (gpointer)id); }

char *vh_save_dialog(const char *suggested) {
    GtkWidget *dialog = gtk_file_chooser_dialog_new(
        "Exporter", GTK_WINDOW(window), GTK_FILE_CHOOSER_ACTION_SAVE,
        "_Annuler", GTK_RESPONSE_CANCEL, "_Enregistrer", GTK_RESPONSE_ACCEPT, NULL);
    GtkFileChooser *chooser = GTK_FILE_CHOOSER(dialog);
    gtk_file_chooser_set_do_overwrite_confirmation(chooser, TRUE);
    gtk_file_chooser_set_current_name(chooser, suggested);
    char *path = NULL;
    if (gtk_dialog_run(GTK_DIALOG(dialog)) == GTK_RESPONSE_ACCEPT)
        path = gtk_file_chooser_get_filename(chooser);
    gtk_widget_destroy(dialog);
    return path;
}

void vh_free(void *p) { g_free(p); }
