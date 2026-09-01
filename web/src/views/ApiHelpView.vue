<script setup lang="ts">
import { ref } from 'vue'

interface ParamRow {
  name: string
  type: string
  required: string
  description: string
}

interface FieldRow {
  name: string
  type: string
  description: string
}

const copiedKey = ref('')

const authOptions = [
  { label: '请求头', value: 'X-API-Key: YOUR_API_KEY' },
  { label: 'Authorization', value: 'Authorization: YOUR_API_KEY' }
]

const searchParams: ParamRow[] = [
  { name: 'kw', type: 'string', required: '是', description: '搜索关键词。GET 也兼容 q、keyword。' },
  { name: 'q', type: 'string', required: '否', description: 'kw 的兼容别名。' },
  { name: 'res', type: 'string', required: '否', description: '返回结构：merge、results、all。默认 merge。' },
  { name: 'cloud_types', type: 'string[]', required: '否', description: '资源类型或网盘类型，GET 用逗号分隔。支持 cloud_drive、magnet、ed2k、video、quark、baidu、aliyun、uc、xunlei、tianyi、115、mobile、pikpak、123 等。' },
  { name: 'include_image', type: 'boolean', required: '否', description: '返回网盘资源封面图。GET 支持 true/false、1/0、yes/no。默认不返回。' },
  { name: 'include_media_metadata', type: 'boolean', required: '否', description: '返回媒体元数据。GET 支持 true/false、1/0、yes/no。' },
  { name: 'media_metadata', type: 'boolean', required: '否', description: 'include_media_metadata 的兼容别名。' },
  { name: 'limit', type: 'number', required: '否', description: '分页数量，默认 50，最大 3000。' },
  { name: 'offset', type: 'number', required: '否', description: '分页偏移量，默认 0。' }
]

const searchFields: FieldRow[] = [
  { name: 'code', type: 'number', description: '0 表示成功。' },
  { name: 'message', type: 'string', description: '响应说明。' },
  { name: 'data.total', type: 'number', description: '命中的资源总数。' },
  { name: 'data.results', type: 'array', description: 'res=results 或 all 时返回的明细列表。' },
  { name: 'data.merged_by_type', type: 'object', description: 'res=merge 或 all 时返回的按类型聚合结果。' },
  { name: 'media', type: 'object', description: 'include_media_metadata=true 时返回标题、年份、季集、清晰度、大小、TMDB 等字段。' }
]

const healthFields: FieldRow[] = [
  { name: 'service', type: 'string', description: '服务状态。正常时为 ok。' },
  { name: 'version', type: 'string', description: '携带有效 API Key 时返回当前服务版本。' }
]

const mediaParams: ParamRow[] = [
  { name: 'fileid', type: 'number', required: '是', description: 'Telegram 文件 ID，必须是正整数。' },
  { name: 'exp', type: 'string', required: '签名访问必填', description: '签名 URL 的过期时间戳，由搜索结果里的媒体 URL 自动携带。' },
  { name: 'sig', type: 'string', required: '签名访问必填', description: '媒体 URL 签名，由搜索结果里的媒体 URL 自动携带。' }
]

const linkCheckParams: ParamRow[] = [
  { name: 'items', type: 'array', required: '是', description: '待检测链接数组。' },
  { name: 'items[].disk_type', type: 'string', required: '是', description: '网盘类型，例如 quark、aliyun、baidu、tianyi、xunlei、115、123、uc、mobile。' },
  { name: 'items[].url', type: 'string', required: '是', description: '完整分享链接。' },
  { name: 'items[].password', type: 'string', required: '否', description: '提取码，未包含在链接里时可传。' },
  { name: 'timeout', type: 'number', required: '否', description: '整次检测超时时间，单位秒，默认 5。' },
  { name: 'timeout_ms', type: 'number', required: '否', description: '整次检测超时时间，单位毫秒；同时传入时优先于 timeout。' }
]

const linkCheckFields: FieldRow[] = [
  { name: 'data.timeout_ms', type: 'number', description: '本次检测使用的总超时时间，单位毫秒。' },
  { name: 'data.results', type: 'array', description: '按请求顺序返回的检测结果。' },
  { name: 'data.grouped', type: 'object', description: '按 disk_type 分组的检测结果，便于外部系统直接消费。' },
  { name: 'state', type: 'string', description: '检测状态：ok、bad、locked、unsupported、uncertain、rate_limited。' },
  { name: 'summary', type: 'string', description: '面向调用方展示的简短状态说明。' },
  { name: 'size_bytes', type: 'number', description: '网盘接口报告的分享大小，单位字节；也可由一次根目录请求中的文件/文件夹大小累加。' }
]

const linkCheckStates: FieldRow[] = [
  { name: 'ok', type: '有效', description: '链接有效。' },
  { name: 'bad', type: '失效', description: '链接失效、过期、删除或被取消。' },
  { name: 'locked', type: '受限', description: '需要提取码，或提取码错误/缺失。' },
  { name: 'unsupported', type: '不支持', description: '当前网盘类型暂不支持检测。' },
  { name: 'uncertain', type: '不确定', description: '请求失败、超时或无法确认状态。' },
  { name: 'rate_limited', type: '限流', description: '被网盘风控/频控拦截，链接状态未知，不可视为失效。' }
]

const getSearchExample = `curl -G 'http://localhost:9900/api/search' \\
  -H 'X-API-Key: YOUR_API_KEY' \\
  --data-urlencode 'kw=ubuntu' \\
  --data-urlencode 'res=merge' \\
  --data-urlencode 'cloud_types=quark,aliyun' \\
  --data-urlencode 'include_image=true' \\
  --data-urlencode 'limit=50'`

const postSearchExample = `curl -X POST 'http://localhost:9900/api/search' \\
  -H 'Content-Type: application/json' \\
  -H 'Authorization: YOUR_API_KEY' \\
  -d '{
    "kw": "ubuntu",
    "res": "all",
    "cloud_types": ["quark", "aliyun"],
    "include_image": true,
    "include_media_metadata": true,
    "limit": 50,
    "offset": 0
  }'`

const searchResponseExample = `{
  "code": 0,
  "message": "success",
  "data": {
    "total": 126,
    "merged_by_type": {
      "quark": [
        {
          "url": "https://pan.quark.cn/s/42455f092f5d",
          "note": "迷墙",
          "datetime": "2026-06-09T20:05:21Z",
          "images": [
            "/i/4986016461960711126?exp=1781131335&sig=4bdb7be40232890fbe159fc2cfa9753ff5016bc9e2a35180219eb6760ae8ba7b"
          ],
          "media": {
            "title": "迷墙",
            "year": "2026",
            "episode": "更新07集",
            "quality": "4K",
            "tags": "迷墙 leoziyuan"
          }
        }
      ]
    }
  }
}`

const searchResultsResponseExample = `{
  "code": 0,
  "message": "success",
  "data": {
    "total": 43,
    "results": [
      {
        "unique_id": "link:https://pan.quark.cn/s/42455f092f5d",
        "datetime": "2026-06-09T20:05:21Z",
        "title": "迷墙 更新07集 国语中字 2026 4K【国剧】",
        "links": [
          {
            "type": "quark",
            "url": "https://pan.quark.cn/s/42455f092f5d",
            "datetime": "2026-06-09T20:05:21Z",
            "work_title": "迷墙 更新07集 国语中字 2026 4K【国剧】"
          }
        ]
      }
    ]
  }
}`

const healthExample = `curl 'http://localhost:9900/api/health' \\
  -H 'X-API-Key: YOUR_API_KEY'`

const videoExample = `curl 'http://localhost:9900/v/202001' \\
  -H 'X-API-Key: YOUR_API_KEY' \\
  -H 'Range: bytes=0-'`

const imageExample = `curl 'http://localhost:9900/i/201001' \\
  -H 'X-API-Key: YOUR_API_KEY'`

const linkCheckExample = `curl -X POST 'http://localhost:9900/api/check/links' \\
  -H 'Content-Type: application/json' \\
  -H 'X-API-Key: YOUR_API_KEY' \\
  -d '{
    "timeout": 5,
    "timeout_ms": 5000,
    "items": [
      {
        "disk_type": "quark",
        "url": "https://pan.quark.cn/s/xxxx",
        "password": "abcd"
      },
      {
        "disk_type": "aliyun",
        "url": "https://www.alipan.com/s/yyyy"
      }
    ]
  }'`

const linkCheckResponseExample = `{
  "code": 0,
  "message": "success",
  "data": {
    "timeout_ms": 5000,
    "results": [
      {
        "disk_type": "quark",
        "url": "https://pan.quark.cn/s/xxxx",
        "password": "abcd",
        "state": "ok",
        "summary": "链接有效",
        "size_bytes": 42704901049
      }
    ],
    "grouped": {
      "quark": [
        {
          "disk_type": "quark",
          "url": "https://pan.quark.cn/s/xxxx",
          "password": "abcd",
          "state": "ok",
          "summary": "链接有效",
          "size_bytes": 42704901049
        }
      ]
    }
  }
}`

const signedMediaExample = `<video
  src="/v/202001?exp=1735689600&sig=SIGNED_VALUE"
  controls
></video>

<img
  src="/i/201001?exp=1735689600&sig=SIGNED_VALUE"
  alt=""
/>`

async function copyCode(key: string, value: string) {
  try {
    await navigator.clipboard.writeText(value)
  } catch {
    const textarea = document.createElement('textarea')
    textarea.value = value
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    document.body.removeChild(textarea)
  }
  copiedKey.value = key
  window.setTimeout(() => {
    if (copiedKey.value === key) copiedKey.value = ''
  }, 1800)
}
</script>

<template>
  <section class="page-section api-help-page">
    <div class="page-header">
      <div>
        <p class="page-kicker">API</p>
        <h1 class="page-title">公开 API 帮助</h1>
        <p class="page-subtitle">外部搜索、健康检查和 Telegram 媒体访问接口的调用说明。</p>
      </div>
    </div>

    <section class="panel summary-panel">
      <div class="summary-item">
        <span class="method method-get">GET</span>
        <strong>/api/health</strong>
        <small>服务健康检查</small>
      </div>
      <div class="summary-item">
        <span class="method method-mixed">GET/POST</span>
        <strong>/api/search</strong>
        <small>资源搜索</small>
      </div>
      <div class="summary-item">
        <span class="method method-post">POST</span>
        <strong>/api/check/links</strong>
        <small>网盘链接有效性检测</small>
      </div>
      <div class="summary-item">
        <span class="method method-get">GET</span>
        <strong>/v/:fileid</strong>
        <small>视频流</small>
      </div>
      <div class="summary-item">
        <span class="method method-get">GET</span>
        <strong>/i/:fileid</strong>
        <small>图片</small>
      </div>
    </section>

    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">Authentication</p>
          <h2>认证方式</h2>
        </div>
      </div>
      <p class="doc-text">
        <code>/api/search</code> 和 <code>/api/check/links</code> 必须通过请求头携带 API Key。
        <code>/api/health</code> 可携带 API Key 校验密钥并返回版本。<code>/v</code> 和 <code>/i</code> 可以通过请求头携带 API Key，
        也可以直接使用搜索结果中返回的带 <code>exp</code> 与 <code>sig</code> 的签名媒体 URL。
      </p>
      <div class="auth-grid">
        <div v-for="item in authOptions" :key="item.label" class="auth-card">
          <span>{{ item.label }}</span>
          <code>{{ item.value }}</code>
        </div>
      </div>
    </section>

    <section class="panel api-section">
      <div class="endpoint-heading">
        <span class="method method-mixed">GET/POST</span>
        <div>
          <h2>/api/search</h2>
          <p>从本地资源索引中搜索网盘、磁力、ED2K 和视频资源。</p>
        </div>
      </div>

      <h3>请求参数</h3>
      <div class="table-panel">
        <table>
          <thead>
            <tr>
              <th>参数</th>
              <th>类型</th>
              <th>必填</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="param in searchParams" :key="param.name">
              <td><code>{{ param.name }}</code></td>
              <td>{{ param.type }}</td>
              <td>{{ param.required }}</td>
              <td>{{ param.description }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="code-grid">
        <div class="code-card">
          <div class="code-title">
            <strong>GET 示例</strong>
            <button type="button" @click="copyCode('search-get', getSearchExample)">
              {{ copiedKey === 'search-get' ? '已复制' : '复制' }}
            </button>
          </div>
          <pre><code>{{ getSearchExample }}</code></pre>
        </div>
        <div class="code-card">
          <div class="code-title">
            <strong>POST 示例</strong>
            <button type="button" @click="copyCode('search-post', postSearchExample)">
              {{ copiedKey === 'search-post' ? '已复制' : '复制' }}
            </button>
          </div>
          <pre><code>{{ postSearchExample }}</code></pre>
        </div>
      </div>

      <h3>响应字段</h3>
      <div class="table-panel">
        <table>
          <thead>
            <tr>
              <th>字段</th>
              <th>类型</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="field in searchFields" :key="field.name">
              <td><code>{{ field.name }}</code></td>
              <td>{{ field.type }}</td>
              <td>{{ field.description }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="code-grid response-example-grid">
        <div class="code-card">
          <div class="code-title">
            <strong>返回示例（merge）</strong>
            <button type="button" @click="copyCode('search-response', searchResponseExample)">
              {{ copiedKey === 'search-response' ? '已复制' : '复制' }}
            </button>
          </div>
          <pre><code>{{ searchResponseExample }}</code></pre>
        </div>
        <div class="code-card">
          <div class="code-title">
            <strong>返回示例（results）</strong>
            <button type="button" @click="copyCode('search-results-response', searchResultsResponseExample)">
              {{ copiedKey === 'search-results-response' ? '已复制' : '复制' }}
            </button>
          </div>
          <pre><code>{{ searchResultsResponseExample }}</code></pre>
        </div>
      </div>
    </section>

    <section class="panel api-section">
      <div class="endpoint-heading">
        <span class="method method-post">POST</span>
        <div>
          <h2>/api/check/links</h2>
          <p>按需检测网盘分享链接是否仍然可用，适合在外部系统完成搜索后过滤失效链接。</p>
        </div>
      </div>

      <h3>请求参数</h3>
      <div class="table-panel">
        <table>
          <thead>
            <tr>
              <th>参数</th>
              <th>类型</th>
              <th>必填</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="param in linkCheckParams" :key="param.name">
              <td><code>{{ param.name }}</code></td>
              <td>{{ param.type }}</td>
              <td>{{ param.required }}</td>
              <td>{{ param.description }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <p class="doc-text">
        检测采用高并发工作池（默认并发 128）并复用 HTTP/2 连接多路复用，300+ 链接通常可在 1 秒内完成。
        并发数与结果缓存时长可在「设置 → 运行参数 → 链接检测」中调整，保存后立即生效。
        相同链接会在单次请求内去重；确定（<code>ok</code>/<code>bad</code>/<code>locked</code>）结果在进程内缓存
        （默认约 5 分钟，设为 <code>0</code> 关闭），<code>uncertain</code>/<code>unsupported</code> 不缓存。
        超时时间由请求体的 <code>timeout_ms</code>/<code>timeout</code> 指定（默认 5 秒），到期未完成的链接以 <code>uncertain</code> 返回。
        有效结果在网盘接口能报告分享总大小，或一次根目录请求能取得文件/文件夹大小时，会额外返回 <code>size_bytes</code>（单位字节）。
        多个根节点会直接累加，但不会进入子目录；目前支持夸克、115、UC、123、百度和天翼。
      </p>

      <div class="code-grid">
        <div class="code-card">
          <div class="code-title">
            <strong>请求示例</strong>
            <button type="button" @click="copyCode('link-check', linkCheckExample)">
              {{ copiedKey === 'link-check' ? '已复制' : '复制' }}
            </button>
          </div>
          <pre><code>{{ linkCheckExample }}</code></pre>
        </div>
        <div class="code-card">
          <div class="code-title">
            <strong>返回示例</strong>
            <button type="button" @click="copyCode('link-check-response', linkCheckResponseExample)">
              {{ copiedKey === 'link-check-response' ? '已复制' : '复制' }}
            </button>
          </div>
          <pre><code>{{ linkCheckResponseExample }}</code></pre>
        </div>
      </div>

      <h3>响应字段</h3>
      <div class="table-panel">
        <table>
          <thead>
            <tr>
              <th>字段</th>
              <th>类型</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="field in linkCheckFields" :key="field.name">
              <td><code>{{ field.name }}</code></td>
              <td>{{ field.type }}</td>
              <td>{{ field.description }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <h3>状态说明</h3>
      <div class="table-panel">
        <table>
          <thead>
            <tr>
              <th>状态</th>
              <th>含义</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="state in linkCheckStates" :key="state.name">
              <td><code>{{ state.name }}</code></td>
              <td>{{ state.type }}</td>
              <td>{{ state.description }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="panel api-section">
      <div class="endpoint-heading">
        <span class="method method-get">GET</span>
        <div>
          <h2>/api/health</h2>
          <p>用于检查服务进程是否可访问。</p>
        </div>
      </div>

      <div class="code-card single-code">
        <div class="code-title">
          <strong>请求示例</strong>
          <button type="button" @click="copyCode('health', healthExample)">
            {{ copiedKey === 'health' ? '已复制' : '复制' }}
          </button>
        </div>
        <pre><code>{{ healthExample }}</code></pre>
      </div>

      <div class="table-panel">
        <table>
          <thead>
            <tr>
              <th>字段</th>
              <th>类型</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="field in healthFields" :key="field.name">
              <td><code>{{ field.name }}</code></td>
              <td>{{ field.type }}</td>
              <td>{{ field.description }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="panel api-section">
      <div class="endpoint-heading">
        <span class="method method-get">GET</span>
        <div>
          <h2>/v/:fileid</h2>
          <p>读取 Telegram 消息中的视频文件，支持浏览器 Range 分段请求。</p>
        </div>
      </div>

      <h3>路径与查询参数</h3>
      <div class="table-panel">
        <table>
          <thead>
            <tr>
              <th>参数</th>
              <th>类型</th>
              <th>必填</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="param in mediaParams" :key="`video-${param.name}`">
              <td><code>{{ param.name }}</code></td>
              <td>{{ param.type }}</td>
              <td>{{ param.required }}</td>
              <td>{{ param.description }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="code-card single-code">
        <div class="code-title">
          <strong>请求示例</strong>
          <button type="button" @click="copyCode('video', videoExample)">
            {{ copiedKey === 'video' ? '已复制' : '复制' }}
          </button>
        </div>
        <pre><code>{{ videoExample }}</code></pre>
      </div>
    </section>

    <section class="panel api-section">
      <div class="endpoint-heading">
        <span class="method method-get">GET</span>
        <div>
          <h2>/i/:fileid</h2>
          <p>读取 Telegram 消息中的图片，适合在外部页面或结果卡片中展示缩略图。</p>
        </div>
      </div>

      <h3>路径与查询参数</h3>
      <div class="table-panel">
        <table>
          <thead>
            <tr>
              <th>参数</th>
              <th>类型</th>
              <th>必填</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="param in mediaParams" :key="`image-${param.name}`">
              <td><code>{{ param.name }}</code></td>
              <td>{{ param.type }}</td>
              <td>{{ param.required }}</td>
              <td>{{ param.description }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="code-grid">
        <div class="code-card">
          <div class="code-title">
            <strong>请求示例</strong>
            <button type="button" @click="copyCode('image', imageExample)">
              {{ copiedKey === 'image' ? '已复制' : '复制' }}
            </button>
          </div>
          <pre><code>{{ imageExample }}</code></pre>
        </div>
        <div class="code-card">
          <div class="code-title">
            <strong>签名 URL 使用示例</strong>
            <button type="button" @click="copyCode('signed-media', signedMediaExample)">
              {{ copiedKey === 'signed-media' ? '已复制' : '复制' }}
            </button>
          </div>
          <pre><code>{{ signedMediaExample }}</code></pre>
        </div>
      </div>
    </section>
  </section>
</template>

<style scoped>
.api-help-page {
  gap: 18px;
}

.summary-panel {
  display: grid;
  gap: 10px;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
}

.summary-item {
  border: 1px solid var(--app-border-subtle);
  border-radius: var(--app-radius);
  display: grid;
  gap: 6px;
  min-width: 0;
  padding: 12px;
}

.summary-item strong {
  color: var(--app-heading);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 15px;
}

.summary-item small {
  color: var(--app-text-muted);
  font-size: 13px;
}

.panel-heading,
.endpoint-heading {
  align-items: flex-start;
  display: flex;
  gap: 12px;
  justify-content: space-between;
}

.endpoint-heading {
  justify-content: flex-start;
  margin-bottom: 16px;
}

.endpoint-heading h2,
.panel-heading h2 {
  color: var(--app-heading);
  font-size: 17px;
  line-height: 1.35;
  margin: 0;
}

.endpoint-heading p,
.doc-text {
  color: var(--app-text-muted);
  line-height: 1.6;
  margin: 6px 0 0;
}

.doc-text {
  margin-bottom: 14px;
}

.method {
  align-items: center;
  border: 1px solid transparent;
  border-radius: var(--app-radius);
  display: inline-flex;
  flex: 0 0 auto;
  font-size: 12px;
  font-weight: 750;
  justify-content: center;
  line-height: 22px;
  min-width: 52px;
  padding: 0 7px;
}

.method-get {
  background: var(--app-accent-subtle);
  border-color: color-mix(in srgb, var(--app-accent) 30%, var(--app-border));
  color: var(--app-accent);
}

.method-mixed {
  background: var(--app-success-bg);
  border-color: color-mix(in srgb, var(--app-success) 30%, var(--app-border));
  color: var(--app-success);
}

.method-post {
  background: var(--app-warning-bg);
  border-color: color-mix(in srgb, var(--app-warning) 35%, var(--app-border));
  color: var(--app-warning);
}

.auth-grid,
.code-grid {
  display: grid;
  gap: 12px;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
}

.auth-card,
.code-card {
  border: 1px solid var(--app-border-subtle);
  border-radius: var(--app-radius);
  min-width: 0;
}

.auth-card {
  display: grid;
  gap: 7px;
  padding: 12px;
}

.auth-card span {
  color: var(--app-text-muted);
  font-size: 13px;
  font-weight: 650;
}

code,
pre {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

code {
  color: var(--app-heading);
  font-size: 0.94em;
}

.api-section {
  display: grid;
  gap: 14px;
}

.api-section h3 {
  color: var(--app-heading);
  font-size: 15px;
  margin: 0;
}

.code-title {
  align-items: center;
  border-bottom: 1px solid var(--app-border-subtle);
  display: flex;
  gap: 8px;
  justify-content: space-between;
  padding: 9px 10px;
}

.code-title strong {
  color: var(--app-heading);
  font-size: 14px;
}

.code-title button {
  background: var(--app-surface);
  border: 1px solid var(--app-border);
  border-radius: var(--app-radius);
  color: var(--app-text);
  cursor: pointer;
  min-height: 28px;
  padding: 3px 9px;
}

.code-title button:hover {
  background: var(--app-surface-muted);
  border-color: var(--app-border-strong);
}

pre {
  color: var(--app-text);
  font-size: 13px;
  line-height: 1.55;
  margin: 0;
  overflow: auto;
  padding: 12px;
  white-space: pre;
}

.single-code {
  max-width: 760px;
}

.table-panel td {
  min-width: 120px;
}

.table-panel td:last-child {
  min-width: 320px;
}

@media (max-width: 720px) {
  .panel-heading,
  .endpoint-heading {
    display: grid;
  }

  .summary-panel,
  .auth-grid,
  .code-grid {
    grid-template-columns: 1fr;
  }

  .table-panel td {
    min-width: auto;
  }

  .table-panel td:last-child {
    min-width: auto;
  }
}
</style>
