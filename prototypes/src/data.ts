export type Status = 'queued' | 'preparing' | 'running' | 'verifying' | 'confirmed' | 'paused' | 'attention' | 'failed' | 'cancelled'
export const statusLabel: Record<Status, string> = { queued: '待执行', preparing: '准备中', running: '执行中', verifying: '等待验证', confirmed: '平台已确认', paused: '已暂停', attention: '需要处理', failed: '失败', cancelled: '已取消' }
export interface Course { id: number; title: string; subtitle: string; teacher: string; chapters: number; total: number; confirmed: number; cover: string; code: string; term: string }
export const courses: Course[] = [
  { id: 1, title: '设计思维与创新方法', subtitle: '让每一个好想法，都有迹可循。', teacher: '林知远', chapters: 12, total: 32, confirmed: 21, cover: 'design', code: 'DESIGN THINKING', term: '秋季学期' },
  { id: 2, title: '数据结构与算法', subtitle: '从问题出发，构建解法。', teacher: '陈序', chapters: 16, total: 48, confirmed: 30, cover: 'code', code: 'DATA STRUCTURES', term: '秋季学期' },
  { id: 3, title: '大学英语 · 进阶', subtitle: '连接更广阔的世界。', teacher: '许言', chapters: 10, total: 28, confirmed: 19, cover: 'english', code: 'A WORLD OF WORDS', term: '秋季学期' },
  { id: 4, title: '现代艺术赏析', subtitle: '在日常里，发现另一种视角。', teacher: '江晚', chapters: 8, total: 20, confirmed: 8, cover: 'art', code: 'WAYS OF SEEING', term: '秋季学期' },
  { id: 5, title: '高等数学（一）', subtitle: '理解变化，探索无限。', teacher: '周和', chapters: 14, total: 40, confirmed: 40, cover: 'math', code: 'BEYOND THE LIMIT', term: '秋季学期' },
  { id: 6, title: '心理学与生活', subtitle: '从认识自己开始。', teacher: '沈舒', chapters: 8, total: 24, confirmed: 24, cover: 'psych', code: 'MIND & EVERYDAY', term: '秋季学期' },
]
export type AnswerMode = 'cache_first' | 'always_llm'
export const answerModeLabel: Record<AnswerMode, string> = { cache_first: '优先使用已有答案', always_llm: '每次都使用模型' }
export interface QuestionModel { id: string; name: string; description: string; capabilities: string[]; inputPrice: number | null; outputPrice: number | null; enabled: boolean; isDefault: boolean; health: 'healthy' | 'degraded'; cacheEnabled: boolean; cacheScope: 'user' | 'shared'; upstream: string }
export const initialQuestionModels: QuestionModel[] = [
  { id: 'qm-standard', name: '标准模型', description: '日常选择题与简答题，响应更快。', capabilities: ['文本'], inputPrice: null, outputPrice: null, enabled: true, isDefault: true, health: 'healthy', cacheEnabled: true, cacheScope: 'user', upstream: '演示上游 A' },
  { id: 'qm-reasoning', name: '增强模型', description: '复杂推理和多步骤题目，token 用量更高。', capabilities: ['文本'], inputPrice: null, outputPrice: null, enabled: true, isDefault: false, health: 'healthy', cacheEnabled: true, cacheScope: 'user', upstream: '演示上游 B' },
  { id: 'qm-vision', name: '视觉模型', description: '含图片、公式和图表；可自动转述图片。', capabilities: ['文本', '图片'], inputPrice: null, outputPrice: null, enabled: true, isDefault: false, health: 'healthy', cacheEnabled: true, cacheScope: 'user', upstream: '演示上游 C' },
  { id: 'qm-legacy', name: '旧版模型', description: '已停止发布，保留历史查询记录。', capabilities: ['文本'], inputPrice: null, outputPrice: null, enabled: false, isDefault: false, health: 'degraded', cacheEnabled: false, cacheScope: 'user', upstream: '演示上游 A' },
]
export interface QuestionUsage { modelId: string; modelName: string; mode: AnswerMode; cacheHit: boolean; cacheTime?: string; inputTokens: number; outputTokens: number; totalTokens: number; complete: boolean; priceState: 'unpriced' | 'quoted'; costMicros?: number; requestId: string; calls: { slot: string; input: number; output: number; total: number }[] }
export interface Plan { id: string; title: string; courseIds: number[]; types: string[]; priority: string; enabled: boolean; scope: 'all' | 'selected'; chapterIds: string[]; version: number; modelId: string; answerMode: AnswerMode }
export const initialPlans: Plan[] = [
  { id: 'P-01', title: '本周学习安排', courseIds: [1, 2], types: ['视频', '文档', '测验'], priority: '优先处理', enabled: true, scope: 'all', chapterIds: [], version: 1, modelId: 'qm-standard', answerMode: 'cache_first' },
  { id: 'P-02', title: '英语课巩固', courseIds: [3], types: ['视频', '测验'], priority: '普通', enabled: true, scope: 'selected', chapterIds: ['3-1'], version: 1, modelId: 'qm-reasoning', answerMode: 'always_llm' },
  { id: 'P-03', title: '艺术课补充学习', courseIds: [4], types: ['视频', '文档'], priority: '稍后处理', enabled: false, scope: 'all', chapterIds: [], version: 1, modelId: 'qm-standard', answerMode: 'cache_first' },
]
export interface Task { id: string; title: string; courseId: number; owner: string; user: string; planId: string; status: Status; progress: number; observed: string; updated: string; reason?: string; history: { time: string; text: string }[]; question?: QuestionUsage }
const taskRows: [string, number, Status, number, string, string, string?][] = [
  ['3.2 从观察到洞察', 1, 'running', 64, '沈同学', 'P-01'],
  ['4.1 栈与队列', 2, 'running', 38, '沈同学', 'P-01'],
  ['3.3 用户访谈工作坊', 1, 'verifying', 100, '沈同学', 'P-01'],
  ['4.2 循环队列的实现', 2, 'queued', 0, '沈同学', 'P-01'],
  ['Unit 4 · Listening', 3, 'attention', 22, '沈同学', 'P-02', '学习通登录已过期，请重新验证账号。'],
  ['3.1 发现真正的问题', 1, 'confirmed', 100, '沈同学', 'P-01'],
  ['4.3 队列的应用', 2, 'queued', 0, '沈同学', 'P-01'],
  ['2.1 包豪斯的设计语言', 4, 'paused', 18, '沈同学', 'P-03'],
  ['5.2 递归与分治', 2, 'running', 72, '林同学', 'P-04'],
  ['Unit 2 · Reading', 3, 'preparing', 0, '陈同学', 'P-05'],
  ['1.4 现代主义的诞生', 4, 'failed', 42, '许同学', 'P-06', '课程资源暂不可用，已停止执行。'],
  ['2.3 线性表的应用', 2, 'confirmed', 100, '周同学', 'P-07'],
]
export const initialTasks: Task[] = taskRows.map((r, i) => ({ id: `TK-${8124 + i}`, title: r[0], courseId: r[1], status: r[2], progress: r[3], user: r[4], owner: i < 8 ? 'U-1001' : `U-${1001 + i}`, planId: r[5], reason: r[6], observed: r[3] > 0 ? `${Math.floor(r[3] * .22)} 分 ${r[3] % 60} 秒` : '尚未开始', updated: i % 3 === 0 ? '刚刚' : `${i + 2} 秒前`, history: [{ time: '14:21:08', text: '已加入安排，完成任务唯一性检查' }, { time: '14:21:09', text: r[2] === 'queued' ? '等待可用执行名额' : '身份与课程权限检查通过' }, ...(r[2] === 'confirmed' ? [{ time: '14:32:46', text: '平台新鲜查询返回已通过（演示数据）' }] : [])] }))
const quizTasks: Task[] = [
  { id: 'TK-8140', title: '2.3 章节测验', courseId: 2, owner: 'U-1001', user: '沈同学', planId: 'P-01', status: 'confirmed', progress: 100, observed: '答案已确认', updated: '3 分钟前', history: [{ time: '14:10:02', text: '查询题库答案，未命中精确缓存' }, { time: '14:10:06', text: '模型返回答案，记录 token 用量' }, { time: '14:12:40', text: '用户确认答案并提交测验' }, { time: '14:13:11', text: '平台查询返回测验已通过（演示）' }], question: { modelId: 'qm-standard', modelName: '标准模型', mode: 'always_llm', cacheHit: false, inputTokens: 1284, outputTokens: 96, totalTokens: 1380, complete: true, priceState: 'unpriced', requestId: 'qq_demo_8140', calls: [{ slot: 'answer', input: 1284, output: 96, total: 1380 }] } },
  { id: 'TK-8141', title: '3.4 章节测验', courseId: 1, owner: 'U-1001', user: '沈同学', planId: 'P-01', status: 'verifying', progress: 100, observed: '答案等待确认', updated: '刚刚', history: [{ time: '14:28:02', text: '精确缓存命中，跳过模型调用' }, { time: '14:28:02', text: '等待用户确认答案后提交' }], question: { modelId: 'qm-standard', modelName: '标准模型', mode: 'cache_first', cacheHit: true, cacheTime: '10-19 16:20', inputTokens: 0, outputTokens: 0, totalTokens: 0, complete: true, priceState: 'unpriced', requestId: 'qq_demo_8141', calls: [] } },
  { id: 'TK-8142', title: 'Unit 4 · Quiz', courseId: 3, owner: 'U-1001', user: '沈同学', planId: 'P-02', status: 'attention', progress: 0, observed: '尚未查询', updated: '5 分钟前', reason: '所选模型已下架，请重新选择后再作答。', history: [{ time: '14:20:08', text: '安排已切换到增强模型；本任务保留旧模型快照' }, { time: '14:25:12', text: '检测到旧模型不可用，已停止查询' }], question: { modelId: 'qm-legacy', modelName: '旧版模型', mode: 'always_llm', cacheHit: false, inputTokens: 0, outputTokens: 0, totalTokens: 0, complete: false, priceState: 'unpriced', requestId: 'qq_demo_8142', calls: [] } },
]
initialTasks.push(...quizTasks)
export interface Account { id: string; name: string; phone: string; school: string; state: 'valid' | 'expired' | 'limited'; courseCount: number; running: number; balance: number; spent: number; active: string; joined: string; issue?: string }
const names = ['沈知夏', '林予安', '陈思远', '许清禾', '周见山', '宋时宁', '江予白', '陆星遥', '温以棠', '顾知行', '叶青川', '苏晚晴']
export const initialAccounts: Account[] = Array.from({ length: 36 }, (_, i) => ({ id: `U-${1001 + i}`, name: names[i % names.length] + (i >= 12 ? ` ${Math.floor(i / 12) + 1}` : ''), phone: `演示账号 ${String(i + 1).padStart(3, '0')}`, school: ['海岚大学', '青川理工学院', '望川师范大学'][i % 3], state: i % 7 === 3 ? 'expired' : i % 11 === 5 ? 'limited' : 'valid', courseCount: 6 + i % 7, running: initialTasks.filter(t => t.owner === `U-${1001 + i}` && ['running', 'preparing'].includes(t.status)).length, balance: 2450 + i * 85, spent: 1550 + i * 310, active: i < 5 ? '刚刚' : `${i * 2} 分钟前`, joined: `10-${String(20 - i % 18).padStart(2, '0')}`, issue: i % 7 === 3 ? '会话过期' : i % 11 === 5 ? '管理员限制' : undefined }))
export interface Ledger { id: string; label: string; kind: 'recharge' | 'spend' | 'release' | 'adjust'; amount: number; time: string; detail: string }
export const initialLedger: Ledger[] = [
  { id: 'L-0924', label: '余额充值', kind: 'recharge', amount: 3000, time: '10-20 10:42', detail: '支付宝 · 已到账' },
  { id: 'L-0923', label: '本周学习安排', kind: 'spend', amount: -350, time: '10-20 09:18', detail: '历史消费示意 · 价格待确定' },
  { id: 'L-0922', label: '费用预占释放', kind: 'release', amount: 150, time: '10-19 18:36', detail: '任务未执行 · 已释放' },
  { id: 'L-0921', label: '英语课巩固', kind: 'spend', amount: -850, time: '10-19 15:20', detail: '历史消费示意 · 价格待确定' },
  { id: 'L-0000', label: '期初余额', kind: 'adjust', amount: 500, time: '10-01 00:00', detail: '历史结转 · 合成记录' },
]
export interface Order { id: string; user: string; amount: number; channel: string; status: 'pending' | 'paid' | 'credited' | 'closed' | 'abnormal' | 'refunded'; time: string }
export const initialOrders: Order[] = [
  { id: '2026102000101', user: '沈知夏', amount: 3000, channel: '支付宝', status: 'credited', time: '10-20 10:42' },
  { id: '2026102000102', user: '林予安', amount: 5000, channel: '微信支付', status: 'paid', time: '10-20 10:38' },
  { id: '2026102000103', user: '陈思远', amount: 2000, channel: '支付宝', status: 'pending', time: '10-20 10:30' },
  { id: '2026102000104', user: '许清禾', amount: 5000, channel: '微信支付', status: 'abnormal', time: '10-20 09:59' },
  { id: '2026102000105', user: '周见山', amount: 1000, channel: '支付宝', status: 'closed', time: '10-19 21:19' },
  { id: '2026102000106', user: '宋时宁', amount: 3000, channel: '支付宝', status: 'refunded', time: '10-19 18:40' },
]
export const orderLabels = { pending: '待支付', paid: '支付成功 · 待入账', credited: '已到账', closed: '已关闭', abnormal: '待核对', refunded: '已退款' }
export const money = (cents: number) => (cents / 100).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
export const directions = [
  { id: 'graphite', num: '01', name: '石墨', english: 'GRAPHITE', subtitle: '清晰的秩序，专注的工作流。', description: '中性色基底与精密排版，以更紧凑的界面承载复杂调度。', color: '#3e65d9', tags: ['克制专业', '高信息密度', '细线框架'] },
  { id: 'grove', num: '02', name: '青禾', english: 'GROVE', subtitle: '让学习，有条不紊地发生。', description: '暖白与森林绿，舒展的留白，让繁忙的学习回归从容。', color: '#28705a', tags: ['温暖轻量', '消费级体验', '自然留白'] },
  { id: 'indigo', num: '03', name: '靛蓝', english: 'INDIGO', subtitle: '为下一步，留出更多可能。', description: '墨蓝与几何构图，用鲜明品牌感连接学习与行动。', color: '#6255d9', tags: ['品牌鲜明', '几何语言', '清晰对比'] },
]
