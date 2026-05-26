#include "redis/redis_client.hpp"

#include <arpa/inet.h>
#include <netdb.h>
#include <sys/socket.h>
#include <unistd.h>

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <stdexcept>
#include <string>

namespace pine {
namespace redis {

namespace {

std::string encode_bulk(const std::string& s) {
    return "$" + std::to_string(s.size()) + "\r\n" + s + "\r\n";
}

std::string encode_command(const std::vector<std::string>& args) {
    std::string out = "*" + std::to_string(args.size()) + "\r\n";
    for (const auto& arg : args) out += encode_bulk(arg);
    return out;
}

}  // namespace

Client::Client(const std::string& host, int port, const std::string& password, int db) {
    struct addrinfo hints{}, *result = nullptr;
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;

    std::string port_str = std::to_string(port);
    int rc = getaddrinfo(host.c_str(), port_str.c_str(), &hints, &result);
    if (rc != 0) return;

    for (auto* rp = result; rp != nullptr; rp = rp->ai_next) {
        fd_ = socket(rp->ai_family, rp->ai_socktype, rp->ai_protocol);
        if (fd_ < 0) continue;

        struct timeval tv{};
        tv.tv_sec = 2;
        setsockopt(fd_, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
        setsockopt(fd_, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));

        if (connect(fd_, rp->ai_addr, rp->ai_addrlen) == 0) break;
        close(fd_);
        fd_ = -1;
    }
    freeaddrinfo(result);

    if (fd_ < 0) return;

    if (!password.empty()) {
        send_command({"AUTH", password});
        expect_ok();
    }
    if (db != 0) {
        send_command({"SELECT", std::to_string(db)});
        expect_ok();
    }
}

Client::~Client() {
    if (fd_ >= 0) close(fd_);
}

bool Client::connected() const { return fd_ >= 0; }

void Client::send_command(const std::vector<std::string>& args) {
    std::string data = encode_command(args);
    const char* ptr = data.c_str();
    std::size_t remaining = data.size();
    while (remaining > 0) {
        ssize_t n = write(fd_, ptr, remaining);
        if (n <= 0) throw std::runtime_error("redis write failed");
        ptr += n;
        remaining -= static_cast<std::size_t>(n);
    }
}

std::string Client::read_line() {
    std::string line;
    for (;;) {
        char c = read_byte();
        if (c == '\r') {
            char lf = read_byte();
            if (lf != '\n') throw std::runtime_error("redis protocol error");
            return line;
        }
        line.push_back(c);
    }
}

char Client::read_type() { return read_byte(); }

void Client::ensure_bytes(std::size_t need) {
    while (read_buf_.size() - read_pos_ < need) {
        if (read_pos_ > 0 && read_pos_ == read_buf_.size()) {
            // Buffer fully drained; reset to avoid unbounded growth.
            read_buf_.clear();
            read_pos_ = 0;
        }
        char chunk[4096];
        ssize_t n = read(fd_, chunk, sizeof(chunk));
        if (n <= 0) throw std::runtime_error("redis read failed");
        read_buf_.append(chunk, static_cast<std::size_t>(n));
    }
}

char Client::read_byte() {
    ensure_bytes(1);
    return read_buf_[read_pos_++];
}

void Client::read_into(char* dst, std::size_t n) {
    while (n > 0) {
        std::size_t avail = read_buf_.size() - read_pos_;
        if (avail == 0) {
            // Drain directly into the caller buffer — no per-syscall 4 KB
            // staging copy. The previous variant read into a stack chunk
            // and memcpy'd out, paying 2× memory traffic on every large
            // bulk read.
            ssize_t got = read(fd_, dst, n);
            if (got <= 0) throw std::runtime_error("redis read failed");
            dst += got;
            n -= static_cast<std::size_t>(got);
            continue;
        }
        std::size_t take = std::min(avail, n);
        std::memcpy(dst, read_buf_.data() + read_pos_, take);
        read_pos_ += take;
        dst += take;
        n -= take;
    }
}

std::string Client::read_simple_string() {
    return read_line();
}

std::string Client::read_error() {
    return read_line();
}

int64_t Client::read_integer() {
    std::string line = read_line();
    return std::stoll(line);
}

std::optional<std::string> Client::read_bulk_string() {
    std::string len_str = read_line();
    int len = std::stoi(len_str);
    if (len < 0) return std::nullopt;
    std::string data(static_cast<std::size_t>(len), '\0');
    if (len > 0) read_into(&data[0], static_cast<std::size_t>(len));
    char cr = read_byte();
    char lf = read_byte();
    if (cr != '\r' || lf != '\n') {
        throw std::runtime_error("redis protocol error: missing CRLF after bulk");
    }
    return data;
}

std::vector<std::optional<std::string>> Client::read_array() {
    std::string count_str = read_line();
    int count = std::stoi(count_str);
    if (count < 0) return {};
    std::vector<std::optional<std::string>> result;
    for (int i = 0; i < count; ++i) {
        char type = read_type();
        switch (type) {
            case '$': result.push_back(read_bulk_string()); break;
            case '+': result.push_back(read_simple_string()); break;
            case ':': result.push_back(std::to_string(read_integer())); break;
            case '-': throw std::runtime_error("redis error: " + read_error());
            default: throw std::runtime_error("redis: unexpected type in array");
        }
    }
    return result;
}

void Client::expect_ok() {
    char type = read_type();
    switch (type) {
        case '+': read_line(); break;
        case '-': throw std::runtime_error("redis error: " + read_error());
        case '$': read_bulk_string(); break;
        case ':': read_integer(); break;
        default: throw std::runtime_error("redis: unexpected response type");
    }
}

std::optional<std::string> Client::get(const std::string& key) {
    send_command({"GET", key});
    char type = read_type();
    if (type == '$') return read_bulk_string();
    if (type == '-') throw std::runtime_error("redis error: " + read_error());
    if (type == '+') return read_simple_string();
    throw std::runtime_error("redis: unexpected response type for GET");
}

void Client::set(const std::string& key, const std::string& value, int ttl_seconds) {
    if (ttl_seconds > 0) {
        send_command({"SET", key, value, "EX", std::to_string(ttl_seconds)});
    } else {
        send_command({"SET", key, value});
    }
    expect_ok();
}

std::vector<std::string> Client::smembers(const std::string& key) {
    send_command({"SMEMBERS", key});
    char type = read_type();
    if (type == '*') {
        auto arr = read_array();
        std::vector<std::string> result;
        for (auto& item : arr) {
            if (item) result.push_back(std::move(*item));
        }
        return result;
    }
    if (type == '-') throw std::runtime_error("redis error: " + read_error());
    throw std::runtime_error("redis: unexpected response type for SMEMBERS");
}

void Client::sadd(const std::string& key, const std::vector<std::string>& members) {
    std::vector<std::string> args = {"SADD", key};
    for (const auto& m : members) args.push_back(m);
    send_command(args);
    char type = read_type();
    if (type == ':') { read_integer(); return; }
    if (type == '-') throw std::runtime_error("redis error: " + read_error());
    throw std::runtime_error("redis: unexpected response type for SADD");
}

std::vector<std::string> Client::lrange(const std::string& key, int start, int stop) {
    send_command({"LRANGE", key, std::to_string(start), std::to_string(stop)});
    char type = read_type();
    if (type == '*') {
        auto arr = read_array();
        std::vector<std::string> result;
        for (auto& item : arr) {
            if (item) result.push_back(std::move(*item));
        }
        return result;
    }
    if (type == '-') throw std::runtime_error("redis error: " + read_error());
    throw std::runtime_error("redis: unexpected response type for LRANGE");
}

void Client::rpush(const std::string& key, const std::vector<std::string>& values) {
    std::vector<std::string> args = {"RPUSH", key};
    for (const auto& v : values) args.push_back(v);
    send_command(args);
    char type = read_type();
    if (type == ':') { read_integer(); return; }
    if (type == '-') throw std::runtime_error("redis error: " + read_error());
    throw std::runtime_error("redis: unexpected response type for RPUSH");
}

void Client::del(const std::string& key) {
    send_command({"DEL", key});
    char type = read_type();
    if (type == ':') { read_integer(); return; }
    if (type == '-') throw std::runtime_error("redis error: " + read_error());
    throw std::runtime_error("redis: unexpected response type for DEL");
}

void Client::expire(const std::string& key, int seconds) {
    send_command({"EXPIRE", key, std::to_string(seconds)});
    char type = read_type();
    if (type == ':') { read_integer(); return; }
    if (type == '-') throw std::runtime_error("redis error: " + read_error());
    throw std::runtime_error("redis: unexpected response type for EXPIRE");
}

void Client::write_multiexec(const std::vector<std::vector<std::string>>& command_args_list) {
    send_command({"MULTI"});
    char type = read_type();
    if (type == '-') throw std::runtime_error("redis error: " + read_error());
    if (type == '+') { read_line(); } else { throw std::runtime_error("redis: unexpected MULTI response"); }

    for (const auto& args : command_args_list) {
        send_command(args);
        type = read_type();
        if (type == '-') {
            std::string err = read_error();
            // Try to abort transaction if possible
            try { send_command({"DISCARD"}); read_line(); } catch (...) {}
            throw std::runtime_error("redis error: " + err);
        }
        if (type == '+') { read_line(); } else { throw std::runtime_error("redis: unexpected queued response"); }
    }

    send_command({"EXEC"});
    type = read_type();
    if (type == '*') {
        // Read and discard EXEC results
        read_array();
        return;
    }
    if (type == '-') throw std::runtime_error("redis error: " + read_error());
    throw std::runtime_error("redis: unexpected response type for EXEC");
}

}  // namespace redis
}  // namespace pine
