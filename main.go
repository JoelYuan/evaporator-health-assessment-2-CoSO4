// go build -ldflags="-s -w" -o 硫酸钴溶液三效蒸发加热室健康度评估系统.exe main.go
package main

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// === 密度表 (温度℃ → []{七水质量分数%, 密度g/cm³}) ===
var densityTable = map[float64][][2]float64{
	20:  {{0, 1.000}, {10, 1.092}, {15, 1.142}, {20, 1.195}, {25, 1.250}, {30, 1.308}, {35, 1.368}, {40, 1.431}, {45, 1.497}, {48, 1.540}, {50, 1.569}, {51, 1.584}, {52, 1.599}},
	40:  {{0, 1.000}, {15, 1.126}, {20, 1.175}, {25, 1.227}, {30, 1.282}, {35, 1.340}, {40, 1.401}, {45, 1.465}, {48, 1.505}, {50, 1.533}, {51, 1.547}, {52, 1.561}},
	50:  {{0, 1.000}, {20, 1.160}, {25, 1.210}, {30, 1.263}, {35, 1.319}, {40, 1.378}, {45, 1.440}, {48, 1.478}, {50, 1.505}, {51, 1.519}, {52, 1.533}},
	55:  {{0, 1.000}, {30, 1.247}, {34, 1.293}, {38, 1.345}, {42, 1.400}, {46, 1.458}, {49, 1.500}, {50, 1.515}, {51, 1.530}, {51.8, 1.540}},
	60:  {{0, 1.000}, {32, 1.268}, {36, 1.316}, {40, 1.368}, {44, 1.423}, {48, 1.482}, {50, 1.512}, {51, 1.527}, {52, 1.542}, {53, 1.557}},
	80:  {{0, 0.992}, {40, 1.315}, {45, 1.367}, {48, 1.405}, {50, 1.433}, {51, 1.447}, {52, 1.461}},
	100: {{0, 0.980}, {45, 1.330}, {48, 1.365}, {50, 1.392}, {51, 1.405}, {52, 1.418}},
}

// 双向线性插值：温度 + 密度 → 七水合硫酸钴质量分数%
func getConc(temp, density float64) float64 {
	keys := make([]float64, 0, len(densityTable))
	for k := range densityTable {
		keys = append(keys, k)
	}
	sort.Float64s(keys)

	var t1, t2 float64
	for _, t := range keys {
		if t <= temp {
			t1 = t
		}
		if t >= temp && t2 == 0 {
			t2 = t
			break
		}
	}
	if t2 == 0 {
		t2 = t1
	}

	// 在给定温度下的密度-浓度表中插值
	interp := func(tbl [][2]float64, d float64) float64 {
		for i := 0; i < len(tbl)-1; i++ {
			if d >= tbl[i][1] && d <= tbl[i+1][1] {
				a := (d - tbl[i][1]) / (tbl[i+1][1] - tbl[i][1])
				return tbl[i][0] + a*(tbl[i+1][0]-tbl[i][0])
			}
		}
		// 如果密度超出范围，返回边界值
		if d < tbl[0][1] {
			return tbl[0][0]
		}
		return tbl[len(tbl)-1][0]
	}

	c1 := interp(densityTable[t1], density)
	if t1 == t2 {
		return c1
	}
	c2 := interp(densityTable[t2], density)
	return c1 + (c2-c1)/(t2-t1)*(temp-t1)
}

type EffectData struct {
	Qnom1     float64 // I效厂家预设换热能力 kW
	Qnom2     float64 // II效厂家预设换热能力 kW
	Qnom3     float64 // III效厂家预设换热能力 kW
	DtDesign1 float64 // I效预设温差 ℃
	DtDesign2 float64 // II效预设温差 ℃
	DtDesign3 float64 // III效预设温差 ℃
	Qset1     float64 // I效理论蒸发能力 t/h
	Qset2     float64 // II效理论蒸发能力 t/h
	Qset3     float64 // III效理论蒸发能力 t/h
	DtSet1    float64 // I效计划温差 ℃
	DtSet2    float64 // II效计划温差 ℃
	DtSet3    float64 // III效计划温差 ℃
	TempOut1  float64 // I效出料温度 ℃
	TempOut2  float64 // II效出料温度 ℃
	TempOut3  float64 // III效出料温度 ℃
	DensOut1  float64 // I效出料密度 g/cm³
	DensOut2  float64 // II效出料密度 g/cm³
	DensOut3  float64 // III效出料密度 g/cm³
	ConcOut1  float64 // I效自动识别浓度 %
	ConcOut2  float64 // II效自动识别浓度 %
	ConcOut3  float64 // III效自动识别浓度 %
	Qrun1     float64 // I效实际蒸发能力 t/h
	Qrun2     float64 // II效实际蒸发能力 t/h
	Qrun3     float64 // III效实际蒸发能力 t/h
	Health1   float64 // I效健康度 Qrun1/Qset1
	Health2   float64 // II效健康度 Qrun2/Qset2
	Health3   float64 // III效健康度 Qrun3/Qset3
	Status1   string  // I效状态
	Status2   string  // II效状态
	Status3   string  // III效状态
}

type PageData struct {
	Time           string
	FeedConc       float64    // 手动输入的进料浓度
	TargetConc     float64    // 目标浓度（52.5%）
	TotalQset      float64    // 系统峰值脱水能力
	TheoreticalMax float64    // 理论最大投料量
	RecommendLow   float64    // 推荐下限
	RecommendHigh  float64    // 推荐上限
	SuggestFlow    float64    // 建议设定值
	ActualFlow     float64    // 用户实际输入流量
	EffectData     EffectData // 三效数据
}

// 计算水的汽化潜热（kJ/kg）
const LatentHeatOfVaporization = 2257.0 // kJ/kg

// 热负荷(kW) → 蒸发能力(t/h) 的转换
func heatLoadToEvaporation(q_kW float64) float64 {
	return q_kW * 3600.0 / (LatentHeatOfVaporization * 1000.0)
}

func main() {
	http.HandleFunc("/", indexHandler)
	fmt.Println("服务器启动 → http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Time:       time.Now().Format("2006-01-02 15:04:05"),
		TargetConc: 52.5, // 固定目标浓度
		FeedConc:   18.0, // 默认手动输入进料浓度
		ActualFlow: 55.0, // 默认实际流量
		EffectData: EffectData{
			Qnom1: 1200, Qnom2: 1000, Qnom3: 800,
			DtDesign1: 25, DtDesign2: 22, DtDesign3: 18,
			DtSet1: 24, DtSet2: 20, DtSet3: 16,
			TempOut1: 92, TempOut2: 78, TempOut3: 62,
			DensOut1: 1.190, DensOut2: 1.290, DensOut3: 1.550, // 调整后的预设密度
		},
	}

	if r.Method == "POST" {
		// 读取手动输入的进料浓度
		feedConc, err := strconv.ParseFloat(r.FormValue("feed_conc"), 64)
		if err == nil && feedConc > 0 {
			data.FeedConc = feedConc
		}

		// 读取实际流量
		actualFlow, err := strconv.ParseFloat(r.FormValue("actual_flow"), 64)
		if err == nil && actualFlow > 0 {
			data.ActualFlow = actualFlow
		}

		// 读取I效参数
		if qnom1, err := strconv.ParseFloat(r.FormValue("qnom_1"), 64); err == nil && qnom1 > 0 {
			data.EffectData.Qnom1 = qnom1
		}
		if dtDesign1, err := strconv.ParseFloat(r.FormValue("dt_design_1"), 64); err == nil && dtDesign1 > 0 {
			data.EffectData.DtDesign1 = dtDesign1
		}
		if dtSet1, err := strconv.ParseFloat(r.FormValue("dt_set_1"), 64); err == nil && dtSet1 > 0 {
			data.EffectData.DtSet1 = dtSet1
		}
		if tempOut1, err := strconv.ParseFloat(r.FormValue("temp_1"), 64); err == nil && tempOut1 > 0 {
			data.EffectData.TempOut1 = tempOut1
		}
		if densOut1, err := strconv.ParseFloat(r.FormValue("dens_1"), 64); err == nil && densOut1 > 0 {
			data.EffectData.DensOut1 = densOut1
		}

		// 读取II效参数
		if qnom2, err := strconv.ParseFloat(r.FormValue("qnom_2"), 64); err == nil && qnom2 > 0 {
			data.EffectData.Qnom2 = qnom2
		}
		if dtDesign2, err := strconv.ParseFloat(r.FormValue("dt_design_2"), 64); err == nil && dtDesign2 > 0 {
			data.EffectData.DtDesign2 = dtDesign2
		}
		if dtSet2, err := strconv.ParseFloat(r.FormValue("dt_set_2"), 64); err == nil && dtSet2 > 0 {
			data.EffectData.DtSet2 = dtSet2
		}
		if tempOut2, err := strconv.ParseFloat(r.FormValue("temp_2"), 64); err == nil && tempOut2 > 0 {
			data.EffectData.TempOut2 = tempOut2
		}
		if densOut2, err := strconv.ParseFloat(r.FormValue("dens_2"), 64); err == nil && densOut2 > 0 {
			data.EffectData.DensOut2 = densOut2
		}

		// 读取III效参数
		if qnom3, err := strconv.ParseFloat(r.FormValue("qnom_3"), 64); err == nil && qnom3 > 0 {
			data.EffectData.Qnom3 = qnom3
		}
		if dtDesign3, err := strconv.ParseFloat(r.FormValue("dt_design_3"), 64); err == nil && dtDesign3 > 0 {
			data.EffectData.DtDesign3 = dtDesign3
		}
		if dtSet3, err := strconv.ParseFloat(r.FormValue("dt_set_3"), 64); err == nil && dtSet3 > 0 {
			data.EffectData.DtSet3 = dtSet3
		}
		if tempOut3, err := strconv.ParseFloat(r.FormValue("temp_3"), 64); err == nil && tempOut3 > 0 {
			data.EffectData.TempOut3 = tempOut3
		}
		if densOut3, err := strconv.ParseFloat(r.FormValue("dens_3"), 64); err == nil && densOut3 > 0 {
			data.EffectData.DensOut3 = densOut3
		}
	}

	// 第一部分：计算各效理论蒸发能力
	qSet1 := heatLoadToEvaporation(data.EffectData.Qnom1) * (data.EffectData.DtSet1 / data.EffectData.DtDesign1)
	qSet2 := heatLoadToEvaporation(data.EffectData.Qnom2) * (data.EffectData.DtSet2 / data.EffectData.DtDesign2)
	qSet3 := heatLoadToEvaporation(data.EffectData.Qnom3) * (data.EffectData.DtSet3 / data.EffectData.DtDesign3)

	data.TotalQset = qSet1 + qSet2 + qSet3
	data.EffectData.Qset1 = qSet1
	data.EffectData.Qset2 = qSet2
	data.EffectData.Qset3 = qSet3

	// 计算理论最大投料量和推荐范围
	if data.TargetConc > data.FeedConc {
		concentrationRatio := data.TargetConc / (data.TargetConc - data.FeedConc)
		data.TheoreticalMax = data.TotalQset * concentrationRatio
		data.RecommendLow = data.TheoreticalMax * 0.75
		data.RecommendHigh = data.TheoreticalMax * 0.95
		data.SuggestFlow = data.TheoreticalMax * 0.90
	} else {
		data.TheoreticalMax = 0
		data.RecommendLow = 0
		data.RecommendHigh = 0
		data.SuggestFlow = 0
	}

	// 第二部分：自动识别各效出料浓度（通过双向插值）
	// 密度单位统一为g/cm³，直接使用
	data.EffectData.ConcOut1 = getConc(data.EffectData.TempOut1, data.EffectData.DensOut1)
	data.EffectData.ConcOut2 = getConc(data.EffectData.TempOut2, data.EffectData.DensOut2)
	data.EffectData.ConcOut3 = getConc(data.EffectData.TempOut3, data.EffectData.DensOut3)

	// 使用用户实际输入的流量计算实际蒸发量
	actualFlow := data.ActualFlow

	// I效实际蒸发量（进料浓度为手动输入的FeedConc）
	if data.EffectData.ConcOut1 > data.FeedConc && data.EffectData.ConcOut1 > 0 {
		data.EffectData.Qrun1 = actualFlow * (data.EffectData.ConcOut1 - data.FeedConc) / data.EffectData.ConcOut1
	} else {
		data.EffectData.Qrun1 = 0
	}

	// II效实际蒸发量（进料浓度为I效自动识别的出料浓度）
	cin2 := data.EffectData.ConcOut1
	if data.EffectData.ConcOut2 > cin2 && data.EffectData.ConcOut2 > 0 {
		data.EffectData.Qrun2 = (actualFlow - data.EffectData.Qrun1) * (data.EffectData.ConcOut2 - cin2) / data.EffectData.ConcOut2
	} else {
		data.EffectData.Qrun2 = 0
	}

	// III效实际蒸发量（进料浓度为II效自动识别的出料浓度）
	cin3 := data.EffectData.ConcOut2
	if data.EffectData.ConcOut3 > cin3 && data.EffectData.ConcOut3 > 0 {
		data.EffectData.Qrun3 = (actualFlow - data.EffectData.Qrun1 - data.EffectData.Qrun2) * (data.EffectData.ConcOut3 - cin3) / data.EffectData.ConcOut3
	} else {
		data.EffectData.Qrun3 = 0
	}

	// 计算健康度
	if data.EffectData.Qset1 > 0 {
		data.EffectData.Health1 = data.EffectData.Qrun1 / data.EffectData.Qset1
	} else {
		data.EffectData.Health1 = 0
	}
	if data.EffectData.Qset2 > 0 {
		data.EffectData.Health2 = data.EffectData.Qrun2 / data.EffectData.Qset2
	} else {
		data.EffectData.Health2 = 0
	}
	if data.EffectData.Qset3 > 0 {
		data.EffectData.Health3 = data.EffectData.Qrun3 / data.EffectData.Qset3
	} else {
		data.EffectData.Health3 = 0
	}

	// 状态判断
	switch {
	case data.EffectData.Health1 > 1.1:
		data.EffectData.Status1 = "超负荷运行"
	case data.EffectData.Health1 > 0.9:
		data.EffectData.Status1 = "运行良好"
	case data.EffectData.Health1 > 0.7:
		data.EffectData.Status1 = "轻微结垢"
	case data.EffectData.Health1 > 0.5:
		data.EffectData.Status1 = "中度结垢"
	default:
		data.EffectData.Status1 = "严重结垢"
	}

	switch {
	case data.EffectData.Health2 > 1.1:
		data.EffectData.Status2 = "超负荷运行"
	case data.EffectData.Health2 > 0.9:
		data.EffectData.Status2 = "运行良好"
	case data.EffectData.Health2 > 0.7:
		data.EffectData.Status2 = "轻微结垢"
	case data.EffectData.Health2 > 0.5:
		data.EffectData.Status2 = "中度结垢"
	default:
		data.EffectData.Status2 = "严重结垢"
	}

	switch {
	case data.EffectData.Health3 > 1.1:
		data.EffectData.Status3 = "超负荷运行"
	case data.EffectData.Health3 > 0.9:
		data.EffectData.Status3 = "运行良好"
	case data.EffectData.Health3 > 0.7:
		data.EffectData.Status3 = "轻微结垢"
	case data.EffectData.Health3 > 0.5:
		data.EffectData.Status3 = "中度结垢"
	default:
		data.EffectData.Status3 = "严重结垢"
	}

	// 渲染页面
	tmpl := `
<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>三效蒸发健康度评估</title>
    <style>
        body{font-family:Arial;margin:20px;background:#f8f8f8;}
        .header{background:#4CAF50;color:white;padding:15px;text-align:center;margin-bottom:20px;}
        .summary{background:white;padding:15px;border-radius:8px;margin-bottom:20px;box-shadow: 0 2px 4px rgba(0,0,0,0.1);}
        .row{display:flex;justify-content:space-between;margin-bottom:20px;}
        .col{flex:1;margin:0 10px;border:2px solid #ddd;border-radius:8px;padding:15px;background:white;min-width: 300px;}
        .col h3{text-align:center;margin-top:0;background:#e8f4fd;padding:10px;border-radius:5px;}
        input[type=number]{width:80px;padding:5px;margin:2px;border:1px solid #ccc;border-radius:3px;}
        .ok{background:#d4edda !important;} 
        .warn{background:#fff3cd !important;} 
        .bad{background:#f8d7da !important;}
        .critical{background:#f5c6cb !important; color: #721c24; font-weight: bold;}
        table{width:100%;border-collapse:collapse;margin:10px 0;}
        td,th{border:1px solid #ccc;padding:6px;text-align:center;font-size:14px;}
        .param-row{display:flex;justify-content:space-between;margin:5px 0;}
        .param-item{flex:1;text-align:center;}
        .status-badge{padding:3px 8px;border-radius:12px;font-size:12px;font-weight:bold;}
        .info{background:#d1ecf1;color:#0c5460;padding:8px;border-radius:4px;margin:5px 0;font-size:13px;}
        .highlight{background:#fff3cd;font-weight:bold;}
    </style>
</head>
<body>
    <div class="header">
        <h1>三效蒸发加热室健康度评估系统</h1>
        <p>目标浓度：{{printf "%.2f" .TargetConc}}% | 汽化潜热：2257 kJ/kg</p>
    </div>

    <form method="POST">
        <div class="summary">
            <h3>第一部分：开机投料推荐</h3>
            <table>
                <tr>
                    <td>系统峰值脱水能力 ΣQ_set</td>
                    <td class="highlight">{{printf "%.1f" .TotalQset}} t/h</td>
                    <td>手动输入进料浓度</td>
                    <td><input name="feed_conc" value="{{printf "%.2f" .FeedConc}}" step="0.1"> %</td>
                </tr>
                <tr>
                    <td>目标浓度</td>
                    <td>{{printf "%.2f" .TargetConc}} %</td>
                    <td>理论最大投料量</td>
                    <td>{{printf "%.1f" .TheoreticalMax}} t/h</td>
                </tr>
                <tr>
                    <td>推荐投料范围（安全+高效）</td>
                    <td class="highlight">{{printf "%.1f" .RecommendLow}} ~ {{printf "%.1f" .RecommendHigh}} t/h</td>
                    <td>建议设定值</td>
                    <td class="highlight">{{printf "%.1f" .SuggestFlow}} t/h（90%负荷，最优经济点）</td>
                </tr>
                <tr>
                    <td>用户实际输入流量</td>
                    <td><input name="actual_flow" value="{{printf "%.1f" .ActualFlow}}" step="0.1"> t/h</td>
                    <td>当前时间</td>
                    <td>{{.Time}}</td>
                </tr>
            </table>
        </div>

        <div class="summary">
            <h3>第二部分：每效健康度评估</h3>
            <div class="info">
                <strong>说明：</strong>基于实际运行参数，调整温差Δt_s，ΣQ_set会实时变化，通过实际蒸发量Q_run与理论能力Q_set对比判断加热室健康度
            </div>
            
            <div class="row">
                <div class="col">
                    <h3>I效</h3>
                    <table>
                        <tr><td colspan="2" style="background:#f0f8ff;font-weight:bold;">设备参数</td></tr>
                        <tr><td>厂家预设换热能力 Qnom1</td><td><input name="qnom_1" value="{{printf "%.0f" .EffectData.Qnom1}}" step="10"> kW</td></tr>
                        <tr><td>预设温差 DtDesign1</td><td><input name="dt_design_1" value="{{printf "%.1f" .EffectData.DtDesign1}}" step="0.1"> ℃</td></tr>
                        
                        <tr><td colspan="2" style="background:#f0f8ff;font-weight:bold;">运行参数</td></tr>
                        <tr><td>计划温差 DtSet1</td><td><input name="dt_set_1" value="{{printf "%.1f" .EffectData.DtSet1}}" step="0.1"> ℃</td></tr>
                        <tr><td>出料温度 TempOut1</td><td><input name="temp_1" value="{{printf "%.1f" .EffectData.TempOut1}}" step="0.1"> ℃</td></tr>
                        <tr><td>出料密度 DensOut1</td><td><input name="dens_1" value="{{printf "%.3f" .EffectData.DensOut1}}" step="0.001"> g/cm³</td></tr>
                        
                        <tr><td colspan="2" style="background:#f0f8ff;font-weight:bold;">计算结果</td></tr>
                        <tr><td>自动识别浓度 ConcOut1</td><td>{{printf "%.2f" .EffectData.ConcOut1}} %</td></tr>
                        <tr><td>理论蒸发能力 Qset1</td><td>{{printf "%.2f" .EffectData.Qset1}} t/h</td></tr>
                        <tr><td>实际蒸发能力 Qrun1</td><td>{{printf "%.2f" .EffectData.Qrun1}} t/h</td></tr>
                        <tr><td>健康度 Health1</td><td {{if gt .EffectData.Health1 0.9}}class="ok"
                            {{else if gt .EffectData.Health1 0.7}}class="warn"
                            {{else}}class="bad"{{end}}>
                            {{printf "%.2f" .EffectData.Health1}}
                        </td></tr>
                        <tr><td>状态</td><td>{{.EffectData.Status1}}</td></tr>
                    </table>
                </div>
                
                <div class="col">
                    <h3>II效</h3>
                    <table>
                        <tr><td colspan="2" style="background:#f0f8ff;font-weight:bold;">设备参数</td></tr>
                        <tr><td>厂家预设换热能力 Qnom2</td><td><input name="qnom_2" value="{{printf "%.0f" .EffectData.Qnom2}}" step="10"> kW</td></tr>
                        <tr><td>预设温差 DtDesign2</td><td><input name="dt_design_2" value="{{printf "%.1f" .EffectData.DtDesign2}}" step="0.1"> ℃</td></tr>
                        
                        <tr><td colspan="2" style="background:#f0f8ff;font-weight:bold;">运行参数</td></tr>
                        <tr><td>计划温差 DtSet2</td><td><input name="dt_set_2" value="{{printf "%.1f" .EffectData.DtSet2}}" step="0.1"> ℃</td></tr>
                        <tr><td>出料温度 TempOut2</td><td><input name="temp_2" value="{{printf "%.1f" .EffectData.TempOut2}}" step="0.1"> ℃</td></tr>
                        <tr><td>出料密度 DensOut2</td><td><input name="dens_2" value="{{printf "%.3f" .EffectData.DensOut2}}" step="0.001"> g/cm³</td></tr>
                        
                        <tr><td colspan="2" style="background:#f0f8ff;font-weight:bold;">计算结果</td></tr>
                        <tr><td>自动识别浓度 ConcOut2</td><td>{{printf "%.2f" .EffectData.ConcOut2}} %</td></tr>
                        <tr><td>理论蒸发能力 Qset2</td><td>{{printf "%.2f" .EffectData.Qset2}} t/h</td></tr>
                        <tr><td>实际蒸发能力 Qrun2</td><td>{{printf "%.2f" .EffectData.Qrun2}} t/h</td></tr>
                        <tr><td>健康度 Health2</td><td {{if gt .EffectData.Health2 0.9}}class="ok"
                            {{else if gt .EffectData.Health2 0.7}}class="warn"
                            {{else}}class="bad"{{end}}>
                            {{printf "%.2f" .EffectData.Health2}}
                        </td></tr>
                        <tr><td>状态</td><td>{{.EffectData.Status2}}</td></tr>
                    </table>
                </div>
                
                <div class="col">
                    <h3>III效</h3>
                    <table>
                        <tr><td colspan="2" style="background:#f0f8ff;font-weight:bold;">设备参数</td></tr>
                        <tr><td>厂家预设换热能力 Qnom3</td><td><input name="qnom_3" value="{{printf "%.0f" .EffectData.Qnom3}}" step="10"> kW</td></tr>
                        <tr><td>预设温差 DtDesign3</td><td><input name="dt_design_3" value="{{printf "%.1f" .EffectData.DtDesign3}}" step="0.1"> ℃</td></tr>
                        
                        <tr><td colspan="2" style="background:#f0f8ff;font-weight:bold;">运行参数</td></tr>
                        <tr><td>计划温差 DtSet3</td><td><input name="dt_set_3" value="{{printf "%.1f" .EffectData.DtSet3}}" step="0.1"> ℃</td></tr>
                        <tr><td>出料温度 TempOut3</td><td><input name="temp_3" value="{{printf "%.1f" .EffectData.TempOut3}}" step="0.1"> ℃</td></tr>
                        <tr><td>出料密度 DensOut3</td><td><input name="dens_3" value="{{printf "%.3f" .EffectData.DensOut3}}" step="0.001"> g/cm³</td></tr>
                        
                        <tr><td colspan="2" style="background:#f0f8ff;font-weight:bold;">计算结果</td></tr>
                        <tr><td>自动识别浓度 ConcOut3</td><td>{{printf "%.2f" .EffectData.ConcOut3}} %</td></tr>
                        <tr><td>理论蒸发能力 Qset3</td><td>{{printf "%.2f" .EffectData.Qset3}} t/h</td></tr>
                        <tr><td>实际蒸发能力 Qrun3</td><td>{{printf "%.2f" .EffectData.Qrun3}} t/h</td></tr>
                        <tr><td>健康度 Health3</td><td {{if gt .EffectData.Health3 0.9}}class="ok"
                            {{else if gt .EffectData.Health3 0.7}}class="warn"
                            {{else}}class="bad"{{end}}>
                            {{printf "%.2f" .EffectData.Health3}}
                        </td></tr>
                        <tr><td>状态</td><td>{{.EffectData.Status3}}</td></tr>
                    </table>
                </div>
            </div>
        </div>
        
        <div style="text-align:center; padding:20px;">
            <input type="submit" value="刷新计算" style="padding:10px 30px;font-size:16px;">
        </div>
    </form>
</body>
</html>
	`
	tmplParsed := template.Must(template.New("index").Parse(tmpl))
	tmplParsed.Execute(w, data)
}
