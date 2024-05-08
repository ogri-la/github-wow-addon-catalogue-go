# todo 

* bug, noticed a hang while parsing repos asynchronously
    - possibly a goroutine not exiting cleanly, failing to decrement waitgroup?
    - ctrl-c and re-running 'fixed' it.
        - but this sucks. fix it.

* parsing .toc files
    - right now *any* .toc file is being parsed.
        - we want to use `AddonName/AddonName.+` before falling back to `AddonName/SomeBundledAddon.toc`
            - addons that bundle other addons
                - Luxocracy/NeatPlates release=v436
                    - filename=NeatPlates/NeatPlates-BCC.toc
                    - filename=NeatPlates_Slim_Vertical/NeatPlates_Slim_Vertical-WOTLKC.toc
                    - filename=NeatPlates_ClassicPlates/NeatPlates_ClassicPlates-BCC.toc
                    - ...
                    - filename=NeatPlates_BlizzardPlates/NeatPlates_BlizzardPlates-WOTLKC.toc
                    - etc
                - RealUI/RealUI release=2.3.12.72-beta

* addons spreading their releases over several Github releases
    - the parse_repo logic is already quite large
    - opportunity to separate side-effects (fetching release, assets, release.json) and it's parsing

# todo (no particular order)

* last-seen-date is using mixed formatting
    "last-seen-date": "2024-03-31T09:21:41Z"
    "last-seen-date": "2024-04-27T13:27:51.763369906Z"

* validate addons, exclude any from final catalogue
    - this will interfere with debugging
        - perhaps a --validate flag?
            - --skip-validation ?
    - no game tracks detected

* bug, something is managing to do a PARTIAL download and cache it
    - output/a37aaa5de61a4ac20cb749bd6190fcef
    - filename=LogTracker_CharacterData_EU-1.0.4-202303061816-bcc.zip
    .zips should bypass regular caching until their snippets have been read and the stored as a json blob

* command, report/diff
    - diff between input and output
        - new addons
        - missing addons
        - updated addons
            - exclude 'last-updated-date' value

* command 'find_duplicates'
    - appears to be grouping addons by what strongbox calls 'sources'
        - i.e., github, wowi, curse, wago, etc
        - in this case, an addon may have multiple sources even within github ...
            - which is probably almost always a *b*
                - capturing fork status is definitely something we should do
                    - capturing the 'canonical' addon when forks exist should be pushed to moderation

* command 'prune'
    - appears to filter out addons that haven't been seen for several runs
        - our catalogue builder does this for us
    - I do want to prune the file cache however
        - default values should be similar to caching defaults
            - do not prune release.json files
            - do not prune .zip file entries (.toc files)
            - prune search results
            - prune release pages

* improve test coverage

* archived/blocked/forked repos
    - I guess they don't appear in search results?
    - more examples necessary

* capture whether an addon is a 'fork' or not
    - see command 'find duplicates'

* capture repo downloads and topics
    - this data would involve another call to the repository info page
        - it would also provide a path to selectively updating the catalogue
            - for example, when a project isn't found in the github search results 
    - topics are normalised and become 'tags'

* shift blacklist/moderation outside of script
    - if something is being excluded, I want a paper trail of when it was excluded and why

