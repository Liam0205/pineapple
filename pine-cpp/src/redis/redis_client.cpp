#include "redis/redis_client.hpp"

#include <arpa/inet.h>
#include <netdb.h>
#include <sys/socket.h>
#include <unistd.h>

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
    char c;
    while (true) {
        ssize_t n = read(fd_, &c, 1);
        if (n <= 0) throw std::runtime_error("redis read failed");
        if (c == '\r') {
            n = read(fd_, &c, 1);
            if (n <= 0 || c != '\n') throw std::runtime_error("redis protocol error");
            return line;
        }
        line.push_back(c);
    }
}

char Client::read_type() {
    char c;
    ssize_t n = read(fd_, &c, 1);
    if (n <= 0) throw std::runtime_error("redis read failed");
    return c;
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
    std::size_t pos = 0;
    while (pos < data.size()) {
        ssize_t n = read(fd_, &data[pos], data.size() - pos);
        if (n <= 0) throw std::runtime_error("redis read failed");
        pos += static_cast<std::size_t>(n);
    }
    char cr, lf;
    read(fd_, &cr, 1);
    read(fd_, &lf, 1);
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

}  // namespace redis
}  // namespace pine
