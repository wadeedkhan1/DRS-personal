import React, { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Shield, Copy, Check, Monitor, Smartphone, AlertCircle, Download } from 'lucide-react';
import { useAgentDownload } from '../hooks/useAgentDownload';

/**
 * Where an invite link lands when somebody clicks it.
 *
 * EnrollDeviceModal hands out `${origin}/enroll?token=…`. The link is also what gets
 * pasted into the agent, but people click links, and before this route existed that click
 * produced a blank page.
 *
 * This page does not enroll anything — enrollment is the agent posting to
 * /api/devices/enroll from the machine being enrolled, sending its real hostname and OS
 * and receiving a device secret it stores locally. A browser cannot do that. What the page
 * does is hand over the two things the recipient needs: the agent binary and the token.
 * Before it served the binary, the token arrived here with no way to obtain the software
 * it belongs to, and the admin had to send the agent through some other channel.
 *
 * It is public because whoever installs an agent generally has no portal account.
 */
export const EnrollLandingPage: React.FC = () => {
  const [params] = useSearchParams();
  const token = params.get('token');
  const [copied, setCopied] = useState(false);

  const windowsAgent = useAgentDownload('windows');
  const androidAgent = useAgentDownload('android');

  const copy = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  // Both probes have answered and neither binary is there. Distinguished from "still
  // checking" so the page does not flash an error while the requests are in flight.
  const nothingToDownload =
    windowsAgent.available === false && androidAgent.available === false;

  const stepLabel = (n: number, text: string) => (
    <div className="flex items-center gap-2.5 mb-3">
      <span className="flex items-center justify-center w-5 h-5 rounded-full bg-sky-500/15 border border-sky-500/30 text-[10px] font-bold text-sky-300 shrink-0">
        {n}
      </span>
      <p className="text-xs font-semibold text-slate-300 uppercase tracking-wider">{text}</p>
    </div>
  );

  return (
    <div className="min-h-screen w-full flex items-center justify-center p-6 bg-[radial-gradient(ellipse_at_top,_var(--tw-gradient-stops))] from-slate-900 via-slate-950 to-black">
      <div className="w-full max-w-lg">
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-14 h-14 rounded-2xl bg-gradient-to-br from-sky-500 to-blue-700 shadow-xl shadow-sky-500/25 mb-4">
            <Shield className="w-7 h-7 text-white" />
          </div>
          <h1 className="text-2xl font-bold text-slate-100 tracking-tight">Enroll this device</h1>
          <p className="text-sm text-slate-400 mt-1">
            Connect this machine to the DRS monitoring platform.
          </p>
        </div>

        <div className="rounded-2xl bg-slate-900/80 backdrop-blur-xl border border-slate-800 p-8 shadow-2xl">
          {!token ? (
            <div className="flex items-start gap-3 text-xs text-amber-400">
              <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
              <div className="space-y-1.5">
                <p className="font-semibold">No enrollment token in this link.</p>
                <p className="text-slate-400 leading-relaxed">
                  An invite link looks like <span className="font-mono">/enroll?token=DRS-…</span>.
                  Ask your administrator to send you a new one.
                </p>
              </div>
            </div>
          ) : (
            <>
              {/* Step 1 — the software. First because the token is useless without it. */}
              <div className="mb-7">
                {stepLabel(1, 'Download the agent')}

                <div className="space-y-2">
                  {windowsAgent.available && (
                    <a
                      href={windowsAgent.url}
                      className="flex items-center gap-3 p-3.5 rounded-xl bg-slate-800/60 hover:bg-slate-800 border border-slate-700/60 hover:border-sky-500/50 transition-all group"
                    >
                      <Monitor className="w-4 h-4 text-sky-400 shrink-0" />
                      <div className="flex-1 min-w-0">
                        <div className="text-sm font-semibold text-slate-200">
                          Windows agent
                        </div>
                        <div className="text-[11px] text-slate-500">
                          Windows 10, 11 and Server · drs-agent.exe
                        </div>
                      </div>
                      <Download className="w-4 h-4 text-slate-500 group-hover:text-sky-400 shrink-0 transition-colors" />
                    </a>
                  )}

                  {androidAgent.available && (
                    <a
                      href={androidAgent.url}
                      className="flex items-center gap-3 p-3.5 rounded-xl bg-slate-800/60 hover:bg-slate-800 border border-slate-700/60 hover:border-emerald-500/50 transition-all group"
                    >
                      <Smartphone className="w-4 h-4 text-emerald-400 shrink-0" />
                      <div className="flex-1 min-w-0">
                        <div className="text-sm font-semibold text-slate-200">Android agent</div>
                        <div className="text-[11px] text-slate-500">
                          Android 8.0+ · drs-agent.apk
                        </div>
                      </div>
                      <Download className="w-4 h-4 text-slate-500 group-hover:text-emerald-400 shrink-0 transition-colors" />
                    </a>
                  )}

                  {nothingToDownload && (
                    <div className="p-3.5 rounded-xl bg-slate-800/40 border border-slate-700/60 flex items-start gap-2.5">
                      <AlertCircle className="w-3.5 h-3.5 text-slate-500 mt-0.5 shrink-0" />
                      <p className="text-[11px] text-slate-400 leading-relaxed">
                        No agent is published on this server yet. Your administrator will need
                        to send you the DRS Agent directly — your code below still works once
                        you have it.
                      </p>
                    </div>
                  )}
                </div>

                {(windowsAgent.available || androidAgent.available) && (
                  <p className="text-[11px] text-slate-500 mt-2.5 leading-relaxed">
                    Windows may warn that the publisher is unrecognised — the agent is not
                    code-signed. Choose <span className="text-slate-400">More info</span> then{' '}
                    <span className="text-slate-400">Run anyway</span>.
                  </p>
                )}
              </div>

              {/* Step 2 — the credential. */}
              <div className="mb-7 pt-6 border-t border-slate-800">
                {stepLabel(2, 'Copy your enrollment code')}
                <div className="flex items-center gap-2">
                  <code className="flex-1 px-3.5 py-3 rounded-xl bg-slate-950/80 border border-slate-800 text-sm font-mono text-sky-300 break-all">
                    {token}
                  </code>
                  <button
                    onClick={() => copy(token)}
                    title="Copy code"
                    className="p-3 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700/60 transition-colors shrink-0"
                  >
                    {copied ? (
                      <Check className="w-4 h-4 text-emerald-400" />
                    ) : (
                      <Copy className="w-4 h-4" />
                    )}
                  </button>
                </div>
              </div>

              {/* Step 3 — what to actually do with the two. */}
              <div className="space-y-4 pt-6 border-t border-slate-800">
                {stepLabel(3, 'Install and connect')}

                <div className="flex items-start gap-3">
                  <Monitor className="w-4 h-4 text-sky-400 mt-0.5 shrink-0" />
                  <div className="text-xs text-slate-400 leading-relaxed">
                    <span className="text-slate-200 font-semibold">Windows —</span> run the DRS
                    Agent on the PC you want monitored, paste this link or code into it, choose
                    whether to share the screen and terminal, and click Enroll.
                  </div>
                </div>

                <div className="flex items-start gap-3">
                  <Smartphone className="w-4 h-4 text-emerald-400 mt-0.5 shrink-0" />
                  <div className="text-xs text-slate-400 leading-relaxed">
                    <span className="text-slate-200 font-semibold">Android —</span> install the
                    APK on the handset, open it, and paste this code when prompted.
                  </div>
                </div>

                <div className="p-3.5 rounded-xl bg-amber-500/[0.06] border border-amber-500/20 flex items-start gap-2.5">
                  <AlertCircle className="w-3.5 h-3.5 text-amber-400 mt-0.5 shrink-0" />
                  <p className="text-[11px] text-slate-400 leading-relaxed">
                    Run the agent on the device you want monitored — opening this page in a
                    browser does not enroll anything. Treat the code like a password: anyone
                    holding it can enroll a device into this organization.
                  </p>
                </div>
              </div>
            </>
          )}

          <div className="mt-6 pt-6 border-t border-slate-800 text-center">
            <Link to="/login" className="text-xs text-sky-400 hover:text-sky-300">
              Administrator sign-in
            </Link>
          </div>
        </div>
      </div>
    </div>
  );
};
