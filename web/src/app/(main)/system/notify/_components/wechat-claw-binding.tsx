"use client";

import { useEffect, useRef, useState } from "react";

import { QRCodeSVG } from "qrcode.react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { WechatClawLoginResult } from "@/lib/types";

const MESSAGES: Record<WechatClawLoginResult["status"], string> = {
  wait: "请使用微信扫码，并在手机上确认授权。二维码 10 分钟内有效。",
  scanned: "已扫码，请在微信中确认授权。",
  activating: "请在微信中打开 ClawBot，发送一条消息完成激活，并保持当前页面打开。",
  confirmed: "绑定并激活成功，保存渠道后即可接收通知。",
  expired: "二维码已过期，请重新扫码。",
  failed: "绑定未完成，请重新扫码。",
};

export function WechatClawBinding({
  config,
  disabled,
  onBound,
  onBusy,
}: {
  config: Record<string, unknown>;
  disabled: boolean;
  onBound: (config: Record<string, unknown>) => void;
  onBusy: (busy: boolean) => void;
}) {
  const [login, setLogin] = useState<{ id: string; qr: string } | null>(null);
  const [status, setStatus] = useState<WechatClawLoginResult["status"]>("wait");
  const [message, setMessage] = useState("");
  const [starting, setStarting] = useState(false);
  const request = useRef<AbortController | null>(null);
  const bound = Boolean(config.bot_token && config.context_token && config.chat_id);
  const busy = starting || (login !== null && ["wait", "scanned", "activating"].includes(status));

  useEffect(() => {
    onBusy(busy);
    return () => onBusy(false);
  }, [busy, onBusy]);
  useEffect(() => () => request.current?.abort(), []);

  useEffect(() => {
    if (!login) return;
    const controller = new AbortController();
    request.current = controller;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const result = await api.notifyWechatClawPoll(login.id, controller.signal);
        if (controller.signal.aborted) return;
        setStatus(result.status);
        setMessage(result.message || MESSAGES[result.status]);
        if (result.status === "confirmed") {
          if (result.config) onBound(result.config);
          else {
            setStatus("failed");
            setMessage("绑定结果缺少配置，请重新扫码。");
          }
        } else if (!["failed", "expired"].includes(result.status)) {
          timer = setTimeout(() => void poll(), 1200);
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setStatus("failed");
          setMessage((error as Error).message);
        }
      }
    };
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [login, onBound]);

  async function start() {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setStarting(true);
    setLogin(null);
    setMessage("正在生成二维码…");
    try {
      const result = await api.notifyWechatClawStart(controller.signal);
      if (controller.signal.aborted) return;
      setStatus("wait");
      setMessage(MESSAGES.wait);
      setLogin({ id: result.login_id, qr: result.qr_content });
    } catch (error) {
      if (!controller.signal.aborted) setMessage((error as Error).message);
    } finally {
      if (!controller.signal.aborted) setStarting(false);
    }
  }

  return (
    <div className="grid gap-3 rounded-lg border p-3">
      <div className="flex items-center justify-between gap-3">
        <span className="font-medium text-sm">微信 Claw 绑定</span>
        <Button type="button" variant="outline" disabled={disabled || starting} onClick={() => void start()}>
          {starting ? "正在生成" : bound || login ? "重新扫码" : "扫码绑定"}
        </Button>
      </div>
      <p className="text-muted-foreground text-xs" role="status" aria-live="polite">
        {message || (bound ? "已保存绑定。登录失效时请重新扫码。" : "扫码确认后，向 ClawBot 发送一条消息完成激活。")}
      </p>
      {login && ["wait", "scanned"].includes(status) && (
        <QRCodeSVG value={login.qr} size={192} marginSize={4} title="微信 Claw 登录二维码" className="mx-auto" />
      )}
      <div className="grid gap-2">
        <Label htmlFor="n-wechat-recipient">iLink 用户 ID</Label>
        <Input id="n-wechat-recipient" value={String(config.chat_id ?? "")} readOnly placeholder="激活后自动填入" />
      </div>
    </div>
  );
}
