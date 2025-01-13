#include <microhttpd.h>
#include <openssl/evp.h>
#include <openssl/x509.h>
#include <openssl/pem.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/*
i686-linux-musl-gcc a100.c -static -o a100_ish \
	-I/i686-linux-musl/include \
  	-L/i686-linux-musl/lib \
	-lmicrohttpd -lssl -lcrypto
 */

#define HTTP_PORT 80
#define HTTPS_PORT 443
#define MB_SIZE 1048576
#define FILE_SIZE_MB 100

static int generate_self_signed_cert(char **cert_pem, char **key_pem) {
    EVP_PKEY *pkey = NULL;
    X509 *x509 = NULL;
    BIO *cert_bio = NULL, *key_bio = NULL;

    // Generate key
    EVP_PKEY_CTX *pctx = EVP_PKEY_CTX_new_id(EVP_PKEY_RSA, NULL);
    if (!pctx || EVP_PKEY_keygen_init(pctx) <= 0 || EVP_PKEY_CTX_set_rsa_keygen_bits(pctx, 2048) <= 0 || EVP_PKEY_keygen(pctx, &pkey) <= 0) {
        fprintf(stderr, "Key generation failed.\n");
        EVP_PKEY_CTX_free(pctx);
        return -1;
    }
    EVP_PKEY_CTX_free(pctx);

    // Generate certificate
    x509 = X509_new();
    if (!x509) {
        fprintf(stderr, "X509 allocation failed.\n");
        EVP_PKEY_free(pkey);
        return -1;
    }
    ASN1_INTEGER_set(X509_get_serialNumber(x509), 1);
    X509_gmtime_adj(X509_get_notBefore(x509), 0);
    X509_gmtime_adj(X509_get_notAfter(x509), 60 * 60 * 24); // 1 day
    X509_set_pubkey(x509, pkey);

    X509_NAME *name = X509_get_subject_name(x509);
    X509_NAME_add_entry_by_txt(name, "CN", MBSTRING_ASC, (unsigned char *)"localhost", -1, -1, 0);
    X509_set_issuer_name(x509, name);

    if (!X509_sign(x509, pkey, EVP_sha256())) {
        fprintf(stderr, "Certificate signing failed.\n");
        X509_free(x509);
        EVP_PKEY_free(pkey);
        return -1;
    }

    // Write to PEM
    cert_bio = BIO_new(BIO_s_mem());
    key_bio = BIO_new(BIO_s_mem());
    PEM_write_bio_X509(cert_bio, x509);
    PEM_write_bio_PrivateKey(key_bio, pkey, NULL, NULL, 0, NULL, NULL);

    long cert_len = BIO_get_mem_data(cert_bio, cert_pem);
    *cert_pem = strndup(*cert_pem, cert_len);

    long key_len = BIO_get_mem_data(key_bio, key_pem);
    *key_pem = strndup(*key_pem, key_len);

    // Clean up
    BIO_free(cert_bio);
    BIO_free(key_bio);
    X509_free(x509);
    EVP_PKEY_free(pkey);
    return 0;
}

// Callback function for file download
static int file_download_callback(void *cls, uint64_t pos, char *buf, size_t max) {
    memset(buf, 0, max); // Fill buffer with zeros
    return (int)max;     // Return the number of bytes written to the buffer
}

static enum MHD_Result handle_request(void *cls, struct MHD_Connection *connection, 
                                      const char *url, const char *method, const char *version, 
                                      const char *upload_data, size_t *upload_data_size, void **con_cls) {

    struct connection_info {
        size_t total_received;
        struct timespec start_time;
    };

    // Initialize connection state on first call
    if (*con_cls == NULL) {
        struct connection_info *conn_info = malloc(sizeof(struct connection_info));
        if (!conn_info) {
            return MHD_NO;
        }
        conn_info->total_received = 0;
        clock_gettime(CLOCK_MONOTONIC, &conn_info->start_time);
        *con_cls = conn_info;
        return MHD_YES; // Indicate that the state is initialized
    }

    struct connection_info *conn_info = *con_cls;

    if (strcmp(method, "GET") == 0 && strcmp(url, "/100-down") == 0) {
        struct MHD_Response *response = MHD_create_response_from_callback(
            FILE_SIZE_MB * MB_SIZE, // Total response size
            MB_SIZE,                // Block size
            file_download_callback, // Callback function
            NULL,                   // User-provided data for the callback
            NULL                    // Cleanup callback (optional)
        );

        MHD_add_response_header(response, "Content-Type", "application/octet-stream");
        MHD_add_response_header(response, "Content-Disposition", "attachment; filename=\"100mb_zeros.bin\"");
        int ret = MHD_queue_response(connection, MHD_HTTP_OK, response);
        MHD_destroy_response(response);
        return ret;
    } else if (strcmp(method, "GET") == 0 && strcmp(url, "/100-up") == 0) {
        const char *html = 
            "<!DOCTYPE html><html><head><title>Upload</title></head>"
            "<body><h1>Upload 100MB</h1><button onclick=\"startUpload()\">Start</button>"
            "<script>"
            "function startUpload() {"
            "  const xhr = new XMLHttpRequest();"
            "  xhr.open('POST', '/100-up', true);"
            "  xhr.send(new Uint8Array(100 * 1024 * 1024));"
            "} "
            "</script></body></html>";

        struct MHD_Response *response = MHD_create_response_from_buffer(strlen(html), (void *)html, MHD_RESPMEM_PERSISTENT);
        MHD_add_response_header(response, "Content-Type", "text/html");
        int ret = MHD_queue_response(connection, MHD_HTTP_OK, response);
        MHD_destroy_response(response);
        return ret;
    } else if (strcmp(method, "POST") == 0 && strcmp(url, "/100-up") == 0) {
        if (*upload_data_size > 0) {
            if (conn_info->total_received == 0) {
                // Record the start time when the upload begins
                clock_gettime(CLOCK_MONOTONIC, &conn_info->start_time);
            }

            // Count the received data
            conn_info->total_received += *upload_data_size;

            // Mark data as processed
            *upload_data_size = 0;

            return MHD_YES;
        }

        // Upload is complete when upload_data_size is 0
        if (conn_info->total_received > 0) {
            // Record the end time
            struct timespec end_time;
            clock_gettime(CLOCK_MONOTONIC, &end_time);

            // Calculate elapsed time
            double elapsed = (end_time.tv_sec - conn_info->start_time.tv_sec) + 
                             (end_time.tv_nsec - conn_info->start_time.tv_nsec) / 1e9;

            // Calculate throughput
            double throughput = (double)conn_info->total_received / (1024 * 1024) / elapsed; // MB/s

            printf("Uploaded %lu bytes\n", conn_info->total_received);
            printf("Elapsed time: %.3f seconds\n", elapsed);
            printf("Throughput: %.2f MB/s\n", throughput);

            // Reset for the next upload
            conn_info->total_received = 0;
        }

        // Send a response to the client
        struct MHD_Response *response = MHD_create_response_from_buffer(strlen("Upload received"), 
                                                                        (void *)"Upload received", 
                                                                        MHD_RESPMEM_PERSISTENT);
        int ret = MHD_queue_response(connection, MHD_HTTP_OK, response);
        MHD_destroy_response(response);

        return ret;
    }

    struct MHD_Response *response = MHD_create_response_from_buffer(strlen("Not Found"), (void *)"Not Found", MHD_RESPMEM_PERSISTENT);
    int ret = MHD_queue_response(connection, MHD_HTTP_NOT_FOUND, response);
    MHD_destroy_response(response);
    return ret;
}

// Cleanup connection state when the connection closes
static void free_request_state(void *con_cls) {
    if (con_cls) {
        free(con_cls);
    }
}

// Function to read a file into memory
char *read_file(const char *filename) {
    FILE *file = fopen(filename, "rb");
    if (!file) {
        perror("Failed to open file");
        return NULL;
    }

    fseek(file, 0, SEEK_END);
    long size = ftell(file);
    rewind(file);

    char *buffer = malloc(size + 1);
    if (!buffer) {
        perror("Failed to allocate memory");
        fclose(file);
        return NULL;
    }

    fread(buffer, 1, size, file);
    buffer[size] = '\0'; // Null-terminate the string
    fclose(file);

    return buffer;
}

int main() {
    struct MHD_Daemon *http_server, *https_server;

    /*
    char *cert_pem, *key_pem;
    if (generate_self_signed_cert(&cert_pem, &key_pem) != 0) {
        fprintf(stderr, "Failed to generate certificates.\n");
        return 1;
    }*/
    // Read key and cert from disk
    char *key_pem = read_file("key.pem");
    char *cert_pem = read_file("cert.pem");

    if (!key_pem || !cert_pem) {
        fprintf(stderr, "Failed to read key or cert\n");
        free(key_pem);
        free(cert_pem);
        return 1;
    }

    http_server = MHD_start_daemon(MHD_USE_SELECT_INTERNALLY, HTTP_PORT, NULL, NULL, &handle_request, NULL, MHD_OPTION_END);
    if (!http_server) {
        fprintf(stderr, "Failed to start HTTP server\n");
        return 1;
    }
/*
    https_server = MHD_start_daemon(
        MHD_USE_SELECT_INTERNALLY | MHD_USE_TLS | MHD_USE_DEBUG, HTTPS_PORT, NULL, NULL,
        &handle_request, NULL,
        MHD_OPTION_HTTPS_MEM_KEY, key_pem,
        MHD_OPTION_HTTPS_MEM_CERT, cert_pem,
        MHD_OPTION_END
    );
    if (!https_server) {
        fprintf(stderr, "Failed to start HTTPS server 2\n");
        MHD_stop_daemon(http_server);
        return 1;
    }*/

    printf("HTTP on %d, HTTPS on %d\n", HTTP_PORT, HTTPS_PORT);
    getchar();

    MHD_stop_daemon(http_server);    // MEMO not much of a performance difference
    //MHD_stop_daemon(https_server); // ERROR on iSH
    free(cert_pem);
    free(key_pem);
    return 0;
}
