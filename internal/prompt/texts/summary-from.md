The user is taking this conversation back to before their message that begins "{{prompt}}", the one marked {{mark}}, setting aside that message and everything after it; the files stay as they are now. Write a summary of the part set aside, which they keep in its place: plain text, at most {{words}} words, and no tool call. Keep:
1. What the user asked for in it, in their own words, and every point where they said to stop or ask first.
2. What was done, and only that: each file a tool call changed and how (those changes are still in the files), the commands that mattered and what they showed.
3. What was found or decided, with its file and line, and what was tried that did not work.
Leave out file contents, long output, and the conversation before that message.[[{{mem?}} Leave out what mem holds (the plan, results, thoughts, facts): gnosh gives you that again.]]
