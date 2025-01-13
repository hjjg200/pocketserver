#include <microhttpd.h>
#include <stdio.h>
#include <string.h>

#define PORT 8080

// Response for GET requests
static enum MHD_Result handle_request(void *cls, struct MHD_Connection *connection,
                                      const char *url, const char *method, const char *version,
                                      const char *upload_data, size_t *upload_data_size, void **con_cls)
{
    const char *response_text = "Hello, World!";
    struct MHD_Response *response;

    // Check if the request is a GET request
    if (strcmp(method, "GET") != 0) {
        return MHD_NO; // Reject non-GET requests
    }

    // Create the response
    response = MHD_create_response_from_buffer(strlen(response_text),
                                               (void *)response_text, MHD_RESPMEM_PERSISTENT);
    if (!response) {
        return MHD_NO;
    }

    // Send the response
    int ret = MHD_queue_response(connection, MHD_HTTP_OK, response);
    MHD_destroy_response(response);
    return ret;
}

int main()
{
    struct MHD_Daemon *server;

    // Start the HTTP server
    server = MHD_start_daemon(MHD_USE_SELECT_INTERNALLY, PORT, NULL, NULL, &handle_request, NULL, MHD_OPTION_END);
    if (!server) {
        fprintf(stderr, "Failed to start HTTP server.\n");
        return 1;
    }

    printf("Server running on http://localhost:%d\n", PORT);

    // Keep the server running
    getchar();

    // Stop the HTTP server
    MHD_stop_daemon(server);

    return 0;
}
