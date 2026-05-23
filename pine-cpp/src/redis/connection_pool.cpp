#include "redis/connection_pool.hpp"

namespace pine {
namespace redis {

std::unique_ptr<Client> ConnectionPool::acquire(const std::string& host, int port,
                                                 const std::string& password, int db) {
    Key key{host, port, password, db};
    {
        std::lock_guard<std::mutex> lk(mu_);
        auto it = idle_.find(key);
        if (it != idle_.end() && !it->second.empty()) {
            auto c = std::move(it->second.back());
            it->second.pop_back();
            return c;
        }
    }
    return std::make_unique<Client>(host, port, password, db);
}

void ConnectionPool::release(const std::string& host, int port,
                              const std::string& password, int db,
                              std::unique_ptr<Client> c) {
    if (!c || !c->connected()) return;
    Key key{host, port, password, db};
    std::lock_guard<std::mutex> lk(mu_);
    idle_[key].push_back(std::move(c));
}

ConnectionPool& shared_pool() {
    static ConnectionPool pool;
    return pool;
}

}  // namespace redis
}  // namespace pine
