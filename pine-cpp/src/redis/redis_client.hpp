#pragma once

#include <optional>
#include <string>
#include <vector>

namespace pine {
namespace redis {

class Client {
public:
    Client(const std::string& host, int port, const std::string& password = "", int db = 0);
    ~Client();

    Client(const Client&) = delete;
    Client& operator=(const Client&) = delete;

    bool connected() const;

    std::optional<std::string> get(const std::string& key);
    void set(const std::string& key, const std::string& value, int ttl_seconds = 0);

    std::vector<std::string> smembers(const std::string& key);
    void sadd(const std::string& key, const std::vector<std::string>& members);

    std::vector<std::string> lrange(const std::string& key, int start, int stop);
    void rpush(const std::string& key, const std::vector<std::string>& values);

    void del(const std::string& key);
    void expire(const std::string& key, int seconds);

    // Multi-Exec helper to wrap write operations under an atomic transaction
    void write_multiexec(const std::vector<std::vector<std::string>>& command_args_list);

private:
    int fd_ = -1;
    // Read buffer to avoid one syscall per byte on response parsing.
    // Sized to a single typical Redis read response (8 KB). The buffer is
    // refilled lazily when the next read needs more bytes than are
    // currently available.
    std::string read_buf_;
    std::size_t read_pos_ = 0;
    void ensure_bytes(std::size_t need);
    char read_byte();
    void read_into(char* dst, std::size_t n);

    void send_command(const std::vector<std::string>& args);
    std::string read_line();
    char read_type();
    std::string read_simple_string();
    std::string read_error();
    int64_t read_integer();
    std::optional<std::string> read_bulk_string();
    std::vector<std::optional<std::string>> read_array();
    void expect_ok();
};

}  // namespace redis
}  // namespace pine
