#include "pine/operator.hpp"

#include <chrono>
#include <cstdint>
#include <memory>

#include "http/http_client.hpp"
#include "http/ssrf.hpp"
#include "operators/_helpers.hpp"

namespace pine {

namespace {

// Best-effort coercion of a JSON value to int64. Mirrors pine-go's
// toInt64Param: accepts number, string-encoded integer, or bool→0/1.
int64_t to_int64_param(const Variant& v) {
  if (v.is_number()) {
    return static_cast<int64_t>(v.as_number());
  }
  if (v.is_bool()) {
    return v.as_bool() ? 1 : 0;
  }
  if (v.is_string()) {
    try {
      return std::stoll(v.as_string());
    } catch (...) {
      return 0;
    }
  }
  return 0;
}

std::vector<std::string> to_string_slice(const Variant& v) {
  std::vector<std::string> out;
  if (!v.is_array()) {
    return out;
  }
  for (const auto& e : v.as_array()) {
    if (e.is_string()) {
      out.push_back(e.as_string());
    }
  }
  return out;
}

}  // namespace

class TransformByRemotePineappleOp : public Operator, public ConcurrentSafe, public ConsumesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    if (!cfg.params.is_object()) {
      throw ExecutionError("transform_by_remote_pineapple: params must be an object");
    }
    const auto& params = cfg.params.as_object();

    std::string host;
    if (auto it = params.find("host"); it != params.end() && it->second.is_string()) {
      host = it->second.as_string();
    }
    int64_t port = 0;
    if (auto it = params.find("port"); it != params.end()) {
      port = to_int64_param(it->second);
    }
    std::string endpoint = "/execute";
    if (auto it = params.find("endpoint"); it != params.end() && it->second.is_string()) {
      const auto& s = it->second.as_string();
      if (!s.empty()) {
        endpoint = s;
      }
    }
    url_ = "http://" + host + ":" + std::to_string(port) + endpoint;
    host_ = host;

    double timeout_seconds = 5.0;
    if (auto it = params.find("timeout"); it != params.end() && it->second.is_number()) {
      timeout_seconds = it->second.as_number();
    }
    timeout_ms_ = static_cast<int64_t>(timeout_seconds * 1000.0);

    fail_on_error_ = true;
    if (auto it = params.find("fail_on_error"); it != params.end() && it->second.is_bool()) {
      fail_on_error_ = it->second.as_bool();
    }

    max_response_size_ = 10 * 1024 * 1024;
    if (auto it = params.find("max_response_size"); it != params.end()) {
      max_response_size_ = to_int64_param(it->second);
    }

    allow_private_ = false;
    if (auto it = params.find("allow_private"); it != params.end() && it->second.is_bool()) {
      allow_private_ = it->second.as_bool();
    }

    if (auto it = params.find("common_request"); it != params.end()) {
      common_req_ = to_string_slice(it->second);
    }
    if (auto it = params.find("item_request"); it != params.end()) {
      item_req_ = to_string_slice(it->second);
    }
    if (auto it = params.find("common_response"); it != params.end()) {
      common_resp_ = to_string_slice(it->second);
    }
    if (auto it = params.find("item_response"); it != params.end()) {
      item_resp_ = to_string_slice(it->second);
    }

    common_input_ = cfg.metadata.common_input;
    common_output_ = cfg.metadata.common_output;
    item_input_ = cfg.metadata.item_input;
    item_output_ = cfg.metadata.item_output;

    if (!allow_private_) {
      std::string reason;
      if (!http::validate_remote_host(host_, &reason)) {
        throw ExecutionError("transform_by_remote_pineapple: " + reason);
      }
    }
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    const auto& common_req_fields = common_req_.empty() ? common_input_ : common_req_;
    const auto& item_req_fields = item_req_.empty() ? item_input_ : item_req_;
    const auto& common_resp_fields = common_resp_.empty() ? common_output_ : common_resp_;
    const auto& item_resp_fields = item_resp_.empty() ? item_output_ : item_resp_;

    Variant::object_t req_common;
    for (std::size_t i = 0; i < common_input_.size(); ++i) {
      if (i >= common_req_fields.size()) {
        break;
      }
      req_common.emplace(common_req_fields[i], input.common(common_input_[i]));
    }

    Variant::array_t req_items;
    req_items.reserve(input.item_count());
    for (std::size_t j = 0; j < input.item_count(); ++j) {
      Variant::object_t item;
      for (std::size_t i = 0; i < item_input_.size(); ++i) {
        if (i >= item_req_fields.size()) {
          break;
        }
        item.emplace(item_req_fields[i], input.item(j, item_input_[i]));
      }
      req_items.emplace_back(std::move(item));
    }

    Variant::object_t req_body_obj;
    req_body_obj.emplace("common", Variant(std::move(req_common)));
    req_body_obj.emplace("items", Variant(std::move(req_items)));
    std::string body = dump_json(Variant(std::move(req_body_obj)), 0);

    http::PostOptions opts;
    opts.url = url_;
    opts.body = std::move(body);
    opts.timeout = std::chrono::milliseconds(timeout_ms_);
    opts.max_response_size = max_response_size_;
    opts.allow_private = allow_private_;

    http::PostResult res = http::post(opts);
    if (!res.ok) {
      handle_error(out, res.error);
      return;
    }
    if (res.status_code != 200) {
      handle_error(out, "transform_by_remote_pineapple: HTTP " + std::to_string(res.status_code) + ": " +
                            truncate_body(res.body));
      return;
    }

    Variant parsed;
    try {
      parsed = parse_json(res.body);
    } catch (const std::exception& e) {
      handle_error(out, std::string("transform_by_remote_pineapple: unmarshal response: ") + e.what());
      return;
    }
    if (!parsed.is_object()) {
      handle_error(out, "transform_by_remote_pineapple: unmarshal response: not an object");
      return;
    }
    const auto& obj = parsed.as_object();

    if (auto it = obj.find("error"); it != obj.end() && it->second.is_string()) {
      const auto& msg = it->second.as_string();
      if (!msg.empty()) {
        handle_error(out, "transform_by_remote_pineapple: downstream error: " + msg);
        return;
      }
    }

    const Variant::object_t* resp_common = nullptr;
    if (auto it = obj.find("common"); it != obj.end() && it->second.is_object()) {
      resp_common = &it->second.as_object();
    }
    const Variant::array_t* resp_items = nullptr;
    if (auto it = obj.find("items"); it != obj.end() && it->second.is_array()) {
      resp_items = &it->second.as_array();
    }

    if (resp_common != nullptr) {
      for (std::size_t i = 0; i < common_output_.size(); ++i) {
        if (i >= common_resp_fields.size()) {
          break;
        }
        auto it = resp_common->find(common_resp_fields[i]);
        if (it != resp_common->end()) {
          out.set_common(common_output_[i], it->second);
        }
      }
    }
    if (resp_items != nullptr) {
      std::size_t n = std::min(input.item_count(), resp_items->size());
      for (std::size_t j = 0; j < n; ++j) {
        if (!(*resp_items)[j].is_object()) {
          continue;
        }
        const auto& item_obj = (*resp_items)[j].as_object();
        for (std::size_t i = 0; i < item_output_.size(); ++i) {
          if (i >= item_resp_fields.size()) {
            break;
          }
          auto it = item_obj.find(item_resp_fields[i]);
          if (it != item_obj.end()) {
            out.set_item(static_cast<int>(j), item_output_[i], it->second);
          }
        }
      }
    }
  }

 private:
  void handle_error(OperatorOutput& out, const std::string& msg) {
    if (fail_on_error_) {
      throw ExecutionError(msg);
    }
    out.set_warning(msg);
  }

  // truncate_body clips a downstream-response body to kErrorBodyMax bytes
  // for inclusion in error messages / warnings. A 5 MB HTML 500 page
  // should not fan out into log / JSON / exception streams as-is.
  static constexpr std::size_t kErrorBodyMax = 1024;
  static std::string truncate_body(const std::string& body) {
    if (body.size() <= kErrorBodyMax) {
      return body;
    }
    return body.substr(0, kErrorBodyMax) + "...(truncated, total " + std::to_string(body.size()) + " bytes)";
  }

  std::string op_name_;
  std::string url_;
  std::string host_;
  int64_t timeout_ms_ = 5000;
  int64_t max_response_size_ = 10 * 1024 * 1024;
  bool fail_on_error_ = true;
  bool allow_private_ = false;

  std::vector<std::string> common_req_;
  std::vector<std::string> item_req_;
  std::vector<std::string> common_resp_;
  std::vector<std::string> item_resp_;

  std::vector<std::string> common_input_;
  std::vector<std::string> common_output_;
  std::vector<std::string> item_input_;
  std::vector<std::string> item_output_;
};

static const OperatorSchema k_transform_by_remote_pineapple_schema{
    .name = "transform_by_remote_pineapple",
    .type = OpType::Transform,
    .description = "Calls a downstream Pineapple service and maps response fields back to the local frame.",
    .params =
        {
            {"host",
             {.type = "string",
              .required = true,
              .default_value = Variant(nullptr),
              .description = "Downstream service host."}},
            {"port",
             {.type = "int64",
              .required = true,
              .default_value = Variant(nullptr),
              .description = "Downstream service port."}},
            {"endpoint",
             {.type = "string",
              .required = false,
              .default_value = Variant("/execute"),
              .description = "Downstream endpoint path."}},
            {"timeout",
             {.type = "float64",
              .required = false,
              .default_value = Variant(5.0),
              .description = "Request timeout in seconds."}},
            {"fail_on_error",
             {.type = "bool",
              .required = false,
              .default_value = Variant(true),
              .description = "true=fatal on downstream error; false=warning and skip."}},
            {"max_response_size",
             {.type = "int64",
              .required = false,
              .default_value = Variant(static_cast<double>(10 * 1024 * 1024)),
              .description = "Maximum response body size in bytes (default 10 MB)."}},
            {"allow_private",
             {.type = "bool",
              .required = false,
              .default_value = Variant(false),
              .description = "Allow connections to private/loopback addresses (dev/internal use)."}},
            {"common_request",
             {.type = "any",
              .required = false,
              .default_value = Variant(nullptr),
              .description = "Downstream common field names, positionally mapped to common_input."}},
            {"item_request",
             {.type = "any",
              .required = false,
              .default_value = Variant(nullptr),
              .description = "Downstream item field names, positionally mapped to item_input."}},
            {"common_response",
             {.type = "any",
              .required = false,
              .default_value = Variant(nullptr),
              .description =
                  "Downstream common response field names, positionally mapped to common_output."}},
            {"item_response",
             {.type = "any",
              .required = false,
              .default_value = Variant(nullptr),
              .description = "Downstream item response field names, positionally mapped to item_output."}},
        },
};
PINE_REGISTER_OPERATOR_T(TransformByRemotePineappleOp, k_transform_by_remote_pineapple_schema)

}  // namespace pine
